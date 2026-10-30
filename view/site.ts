import { initAll, type FrontendOptions } from "./govuk.js";

function initCookies() {
  const banner = document.querySelector<HTMLElement>("[data-cookie-banner]");
  if (!banner || banner.dataset.initialized) return;
  banner.dataset.initialized = "true";
  let dismissed = false;
  try {
    dismissed = document.cookie
      .split(";")
      .some((cookie) => cookie.trim() === "cookie-notice=dismissed-v1");
  } catch {
    // Cookie access can be unavailable when browser storage is blocked.
  }
  banner.hidden = dismissed;
  requireElement("[data-cookie-hide]", banner).addEventListener("click", () => {
    try {
      const secure = location.protocol === "https:" ? "; Secure" : "";
      document.cookie = `cookie-notice=dismissed-v1; Path=/; Max-Age=15552000; SameSite=Lax${secure}`;
    } catch {
      // Dismissing the notice still works for this page without cookie storage.
    }
    banner.hidden = true;
    requireElement("#main-content").focus();
  });
}

type HtmxRequest = {
  elt: HTMLElement;
  xhr: XMLHttpRequest;
  target?: HTMLElement;
};

declare global {
  interface DocumentEventMap {
    "htmx:beforeRequest": CustomEvent<HtmxRequest>;
    "htmx:beforeSwap": CustomEvent<
      HtmxRequest & {
        shouldSwap: boolean;
        isError: boolean;
      }
    >;
    "htmx:afterSwap": CustomEvent<HtmxRequest>;
    "htmx:afterRequest": CustomEvent<HtmxRequest & { failed: boolean }>;
  }
}

type Vote = -1 | 0 | 1;
type VoteResult = { score: number; selected: Vote };
type PageDirection = "prev" | "next";

const frontendOptions = {
  fileUpload: {
    i18n: {
      chooseFilesButton: "Datei auswählen",
      dropInstruction: "oder hier ablegen",
      noFileChosen: "Keine Datei ausgewählt",
      multipleFilesChosen: {
        one: "%{count} Datei ausgewählt",
        other: "%{count} Dateien ausgewählt",
      },
      enteredDropZone: "Datei im Ablagebereich.",
      leftDropZone: "Ablagebereich verlassen.",
    },
  },
  passwordInput: {
    i18n: {
      showPassword: "Anzeigen",
      hidePassword: "Verbergen",
      showPasswordAriaLabel: "Passwort anzeigen",
      hidePasswordAriaLabel: "Passwort verbergen",
      passwordShownAnnouncement: "Dein Passwort ist sichtbar.",
      passwordHiddenAnnouncement: "Dein Passwort ist verborgen.",
    },
  },
} satisfies FrontendOptions;

// These selectors describe required children of our server-rendered components.
function requireElement<T extends HTMLElement = HTMLElement>(
  selector: string,
  root: ParentNode = document,
): T {
  const element = root.querySelector<T>(selector);
  if (!element) {
    throw new Error(`Missing element: ${selector}`);
  }
  return element;
}

function hasModifiers(event: MouseEvent | KeyboardEvent): boolean {
  return event.altKey || event.ctrlKey || event.metaKey || event.shiftKey;
}

function parseVoteResult(value: unknown): VoteResult {
  if (
    typeof value !== "object" ||
    value === null ||
    !("score" in value) ||
    typeof value.score !== "number" ||
    !Number.isInteger(value.score) ||
    !("selected" in value) ||
    (value.selected !== -1 && value.selected !== 0 && value.selected !== 1)
  ) {
    throw new Error("Invalid vote response");
  }
  return { score: value.score, selected: value.selected };
}

async function sendVote(form: HTMLFormElement, vote: string) {
  const body = new URLSearchParams();
  for (const [name, value] of new FormData(form)) {
    body.append(name, String(value));
  }
  body.set("vote", vote);
  const response = await fetch(form.action, {
    method: "POST",
    body,
    headers: { Accept: "application/json" },
  });
  if (response.redirected) {
    window.location.assign(response.url);
    return;
  }
  if (!response.ok) {
    throw new Error("Vote failed");
  }
  return parseVoteResult(await response.json());
}

function renderVote(
  form: HTMLFormElement,
  feedback: HTMLElement,
  result: VoteResult,
) {
  requireElement("[data-vote-score]", form).textContent = String(result.score);
  for (const button of form.querySelectorAll("button")) {
    const value = button.classList.contains("app-vote__button--up") ? 1 : -1;
    const selected = result.selected === value;
    button.setAttribute("aria-pressed", String(selected));
    button.value = String(selected ? 0 : value);
  }
  const messages = {
    [-1]: "Negativ bewertet",
    0: "Bewertung zurückgenommen",
    1: "Positiv bewertet",
  } satisfies Record<Vote, string>;
  feedback.className = "govuk-visually-hidden";
  feedback.textContent = `${form.dataset.voteLabel}: ${messages[result.selected]}. Punktestand: ${result.score}.`;
}

function setVotePending(form: HTMLFormElement, pending: boolean) {
  if (pending) {
    form.dataset.pending = "true";
  } else {
    delete form.dataset.pending;
  }
  for (const button of form.querySelectorAll("button")) {
    if (pending) {
      button.setAttribute("aria-disabled", "true");
    } else {
      button.removeAttribute("aria-disabled");
    }
  }
}

function initVote(form: HTMLFormElement) {
  const container = form.parentElement;
  if (!container) return;
  const feedback = requireElement("[data-vote-feedback]", container);

  form.addEventListener("submit", async (event) => {
    if (!(event.submitter instanceof HTMLButtonElement)) return;
    event.preventDefault();
    if (form.dataset.pending) return;

    setVotePending(form, true);
    feedback.className =
      "govuk-body-s govuk-!-margin-top-1 govuk-!-margin-bottom-0";
    feedback.textContent = "Wird gespeichert …";
    try {
      const result = await sendVote(form, event.submitter.value);
      if (result) renderVote(form, feedback, result);
    } catch {
      feedback.className =
        "govuk-error-message govuk-!-margin-top-1 govuk-!-margin-bottom-0";
      feedback.textContent =
        "Die Bewertung konnte nicht gespeichert werden. Bitte versuche es erneut.";
    } finally {
      setVotePending(form, false);
    }
  });
}

// GOV.UK 6.6.0 references a hidden comma before the visible upload label.
// Remove it so the accessible name follows the visible text's order.
function fixUploadLabels(root: ParentNode) {
  for (const button of root.querySelectorAll(".govuk-file-upload-button")) {
    const separatorId = `${button.id}-comma`;
    const labels = (button.getAttribute("aria-labelledby") ?? "").split(" ");
    button.setAttribute(
      "aria-labelledby",
      labels.filter((id) => id !== separatorId).join(" "),
    );
    document.getElementById(separatorId)?.remove();
  }
}

function createPageButton(
  label: string,
  page: number,
  accessibleLabel: string,
) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "govuk-link govuk-pagination__link";
  button.textContent = label;
  button.dataset.page = String(page);
  button.setAttribute("aria-label", accessibleLabel);
  button.setAttribute("aria-controls", "assignment-modules");
  return button;
}

function createPageIcon(direction: PageDirection) {
  const paths = {
    prev: "m6.5938-0.0078125-6.7266 6.7266 6.7441 6.4062 1.377-1.449-4.1856-3.9768h12.896v-2h-12.984l4.2931-4.293-1.414-1.414z",
    next: "m8.107-0.0078125-1.4136 1.414 4.2926 4.293h-12.986v2h12.896l-4.1855 3.9766 1.377 1.4492 6.7441-6.4062-6.7246-6.7266z",
  };
  const icon = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  icon.setAttribute(
    "class",
    `govuk-pagination__icon govuk-pagination__icon--${direction}`,
  );
  icon.setAttribute("viewBox", "0 0 15 13");
  icon.setAttribute("aria-hidden", "true");
  icon.setAttribute("focusable", "false");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", paths[direction]);
  icon.append(path);
  return icon;
}

function createPageDirection(direction: PageDirection, page: number) {
  const label = direction === "prev" ? "Vorherige" : "Nächste";
  const container = document.createElement("div");
  container.className = `govuk-pagination__${direction}`;
  const button = createPageButton("", page, `${label} Modulseite`);
  const title = document.createElement("span");
  title.className = "govuk-pagination__link-title";
  title.textContent = label;
  const icon = createPageIcon(direction);
  button.append(...(direction === "prev" ? [icon, title] : [title, icon]));
  container.append(button);
  return container;
}

function createPageItem(page: number, current: number) {
  const item = document.createElement("li");
  item.className = "govuk-pagination__item";
  const button = createPageButton(String(page + 1), page, `Seite ${page + 1}`);
  if (page === current) {
    item.classList.add("govuk-pagination__item--current");
    button.setAttribute("aria-current", "page");
  }
  item.append(button);
  return item;
}

function createPageEllipsis() {
  const item = document.createElement("li");
  item.className = "govuk-pagination__item govuk-pagination__item--ellipsis";
  item.textContent = "⋯";
  item.setAttribute("aria-hidden", "true");
  return item;
}

function renderPagination(
  container: HTMLElement,
  current: number,
  pages: number,
) {
  container.replaceChildren();
  container.hidden = pages <= 1;
  if (current > 0) container.append(createPageDirection("prev", current - 1));

  const list = document.createElement("ul");
  list.className = "govuk-pagination__list";
  let previous = -1;
  for (let page = 0; page < pages; page++) {
    if (
      pages > 5 &&
      page !== 0 &&
      page !== pages - 1 &&
      Math.abs(page - current) > 1
    )
      continue;
    if (previous >= 0 && page - previous > 1) list.append(createPageEllipsis());
    list.append(createPageItem(page, current));
    previous = page;
  }
  container.append(list);
  if (current < pages - 1)
    container.append(createPageDirection("next", current + 1));
}

function initAssignmentSearch(root: ParentNode) {
  const search = root.querySelector<HTMLElement>("[data-assignment-search]");
  if (!search || search.dataset.initialized) return;
  const input = requireElement<HTMLInputElement>("input", search);
  const count = requireElement("[data-assignment-count]", search);
  const list = requireElement("#assignment-modules", root);
  const modules = [
    ...root.querySelectorAll<HTMLElement>("[data-assignment-module]"),
  ];
  const pageSize = 10;
  const selectedIndex = modules.findIndex((module) =>
    module.querySelector("input:checked"),
  );
  let page = Math.max(0, Math.floor(selectedIndex / pageSize));

  const pagination = document.createElement("nav");
  pagination.className = "govuk-pagination app-module-pagination";
  pagination.setAttribute("aria-label", "Modulseiten");
  const empty = document.createElement("p");
  empty.className = "govuk-body";
  empty.textContent = "Keine passenden Module gefunden.";
  list.after(empty, pagination);

  const render = () => {
    const term = input.value.trim().toLocaleLowerCase("de");
    const matches = modules.filter((module) =>
      requireElement("label", module)
        .textContent.toLocaleLowerCase("de")
        .includes(term),
    );
    const pages = Math.max(1, Math.ceil(matches.length / pageSize));
    page = Math.min(page, pages - 1);
    const visible = new Set(
      matches.slice(page * pageSize, (page + 1) * pageSize),
    );
    modules.forEach((module) => {
      module.hidden = !visible.has(module);
    });
    empty.hidden = matches.length > 0;
    const selected = modules.filter((module) =>
      module.querySelector("input:checked"),
    ).length;
    const position = pages > 1 ? ` Seite ${page + 1} von ${pages}.` : "";
    count.textContent = `${matches.length} Module gefunden. ${selected} ausgewählt.${position}`;
    renderPagination(pagination, page, pages);
  };

  input.addEventListener("input", () => {
    page = 0;
    render();
  });
  list.addEventListener("change", render);
  pagination.addEventListener("click", (event) => {
    if (!(event.target instanceof Element)) return;
    const button = event.target.closest<HTMLButtonElement>("button[data-page]");
    if (!button) return;
    page = Number(button.dataset.page);
    render();
    requireElement("#module_id", root).focus();
  });
  render();
  search.dataset.initialized = "true";
  search.hidden = false;
}

function initFrontend(root: ParentNode) {
  initAll({ ...frontendOptions, scope: root });
  fixUploadLabels(root);
  initAssignmentSearch(root);
}

function restoreFiles(root: ParentNode, files: readonly File[]) {
  const input = root.querySelector<HTMLInputElement>('input[name="files"]');
  if (!input) return;
  const transfer = new DataTransfer();
  files.forEach((file) => transfer.items.add(file));
  input.files = transfer.files;
}

// Retain local files across a validation response; they never leave the browser
// until the form is submitted again.
function initUploadResponses() {
  const filesByRequest = new WeakMap<XMLHttpRequest, File[]>();
  document.addEventListener("htmx:beforeRequest", ({ detail }) => {
    if (!detail.elt.matches("form.app-exams-upload")) return;
    const input = requireElement<HTMLInputElement>(
      '[name="files"]',
      detail.elt,
    );
    filesByRequest.set(detail.xhr, [...(input.files ?? [])]);
  });
  document.addEventListener("htmx:beforeSwap", ({ detail }) => {
    if (!filesByRequest.has(detail.xhr) || detail.xhr.status !== 422) return;
    detail.shouldSwap = true;
    detail.isError = false;
  });
  document.addEventListener("htmx:afterSwap", ({ detail }) => {
    const files = filesByRequest.get(detail.xhr);
    if (!files) return;
    filesByRequest.delete(detail.xhr);
    const root = requireElement("#main-content");
    restoreFiles(root, files);
    initFrontend(root);
    root
      .querySelector('input[name="files"]')
      ?.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

function wrapDialogFocus(dialog: HTMLDialogElement, event: KeyboardEvent) {
  if (event.key !== "Tab") return;
  const actions = [
    ...dialog.querySelectorAll<HTMLElement>("button:not([disabled]), a[href]"),
  ].filter((element) => element.getClientRects().length > 0);
  const first = actions.at(0);
  const last = actions.at(-1);
  if (!first || !last) return;
  const onHeading = document.activeElement === dialog.querySelector("h2");
  if (event.shiftKey && (document.activeElement === first || onHeading)) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function initDialog(trigger: HTMLElement) {
  const dialog = document.getElementById(trigger.dataset.dialog ?? "");
  if (
    typeof HTMLDialogElement === "undefined" ||
    !(dialog instanceof HTMLDialogElement)
  )
    return;
  trigger.setAttribute("role", "button");
  trigger.setAttribute("aria-haspopup", "dialog");
  trigger.addEventListener("click", (event) => {
    if (hasModifiers(event)) return;
    event.preventDefault();
    dialog.showModal();
    requireElement("h2", dialog).focus();
  });
  const cancel = requireElement("[data-dialog-cancel]", dialog);
  const close = document.createElement("button");
  close.type = "button";
  close.className = "govuk-button govuk-button--secondary";
  close.textContent = cancel.textContent;
  cancel.replaceWith(close);
  close.addEventListener("click", () => dialog.close());
  dialog.addEventListener("close", () => trigger.focus());
  // Wrap explicitly so Chromium does not move Tab focus into browser chrome.
  dialog.addEventListener("keydown", (event) => wrapDialogFocus(dialog, event));
}

function cloneSlide(slide: HTMLElement) {
  const clone = slide.cloneNode(true);
  if (!(clone instanceof HTMLElement)) throw new Error("Invalid slide clone");
  clone.setAttribute("aria-hidden", "true");
  clone.inert = true;
  clone.dataset.clone = "true";
  return clone;
}

function createSlideTrack(slides: readonly HTMLElement[]) {
  const first = slides.at(0);
  const last = slides.at(-1);
  if (!first || !last) throw new Error("Missing carousel slides");
  const track = document.createElement("div");
  track.className =
    "app-event-carousel__track app-event-carousel__track--instant";
  slides.forEach((slide, index) => {
    slide.hidden = false;
    slide.setAttribute("role", "group");
    slide.setAttribute("aria-roledescription", "Folie");
    slide.setAttribute(
      "aria-label",
      `${index + 1} von ${slides.length}: ${slide.dataset.title}`,
    );
    track.append(slide);
  });
  // Bookends make wrapping seamless without exposing duplicate links.
  track.prepend(cloneSlide(last));
  track.append(cloneSlide(first));
  return track;
}

function centerCarouselDot(dot: HTMLElement) {
  const list = dot.parentElement;
  if (!list) return;
  list.scrollLeft +=
    dot.getBoundingClientRect().left -
    list.getBoundingClientRect().left -
    (list.clientWidth - dot.offsetWidth) / 2;
}

function initSwipe(
  viewport: HTMLElement,
  pause: () => void,
  move: (direction: number) => void,
) {
  let start: { x: number; y: number } | undefined;
  viewport.addEventListener(
    "touchstart",
    (event) => {
      pause();
      const touch = event.touches.item(0);
      start =
        event.touches.length === 1 && touch
          ? { x: touch.clientX, y: touch.clientY }
          : undefined;
    },
    { passive: true },
  );
  viewport.addEventListener(
    "touchend",
    (event) => {
      const origin = start;
      start = undefined;
      const touch = event.changedTouches.item(0);
      if (!origin || !touch || event.touches.length) return;
      const dx = touch.clientX - origin.x;
      const dy = touch.clientY - origin.y;
      if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5)
        move(dx < 0 ? 1 : -1);
    },
    { passive: true },
  );
  viewport.addEventListener("touchcancel", () => {
    start = undefined;
  });
}

function createCarouselNavigation(
  track: HTMLElement,
  slides: readonly HTMLElement[],
  dots: readonly HTMLElement[],
  status: HTMLElement,
  motion: MediaQueryList,
) {
  let current = 0;
  let moving = false;
  const position = (index: number) => {
    track.style.transform = `translateX(-${index * 100}%)`;
  };
  const update = () => {
    slides.forEach((slide, index) => {
      slide.inert = index !== current;
      slide.setAttribute("aria-hidden", String(index !== current));
      dots[index]?.setAttribute("aria-disabled", String(index === current));
    });
    status.textContent = `${current + 1} von ${slides.length}`;
    const dot = dots[current];
    if (dot) centerCarouselDot(dot);
  };
  const settle = () => {
    track.classList.add("app-event-carousel__track--instant");
    position(current + 1);
    moving = false;
  };
  const show = (index: number) => {
    if (moving || index === current) return;
    current = (index + slides.length) % slides.length;
    update();
    if (motion.matches) {
      settle();
      return;
    }
    moving = true;
    // Commit the resting position before enabling the next transition.
    void track.offsetWidth;
    track.classList.remove("app-event-carousel__track--instant");
    position(index + 1);
  };
  track.addEventListener("transitionend", (event) => {
    if (event.target === track && event.propertyName === "transform") settle();
  });
  track.addEventListener("transitioncancel", settle);
  update();
  settle();
  return {
    show,
    settle,
    move: (direction: number) => show(current + direction),
  };
}

function createCarouselRotation(
  button: HTMLElement,
  status: HTMLElement,
  advance: () => void,
) {
  let playing = false;
  let timer: number | undefined;
  const setPlaying = (value: boolean) => {
    playing = value;
    window.clearInterval(timer);
    button.setAttribute(
      "aria-label",
      playing ? "Diashow pausieren" : "Diashow starten",
    );
    button.textContent = playing ? "Pausieren" : "Starten";
    status.setAttribute("aria-live", playing ? "off" : "polite");
    timer = playing ? window.setInterval(advance, 7000) : undefined;
  };
  button.addEventListener("click", () => setPlaying(!playing));
  return { setPlaying, pause: () => setPlaying(false) };
}

function initCarousel(carousel: HTMLElement) {
  const slides = [
    ...carousel.querySelectorAll<HTMLElement>(".app-event-slide"),
  ];
  if (slides.length <= 1) return;
  const controls = requireElement(".app-event-carousel__controls", carousel);
  const viewport = requireElement("#event-slides", carousel);
  const status = requireElement('[role="status"]', controls);
  const dots = [...controls.querySelectorAll<HTMLElement>("[data-slide]")];
  const motion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const track = createSlideTrack(slides);
  viewport.append(track);
  carousel.setAttribute("aria-roledescription", "Karussell");
  carousel.classList.add("app-event-carousel--enhanced");

  const navigation = createCarouselNavigation(
    track,
    slides,
    dots,
    status,
    motion,
  );
  const rotation = createCarouselRotation(
    requireElement("[data-rotation]", controls),
    status,
    () => navigation.move(1),
  );
  controls
    .querySelectorAll<HTMLElement>("[data-direction]")
    .forEach((button) => {
      button.addEventListener("click", () => {
        rotation.pause();
        navigation.move(Number(button.dataset.direction));
      });
    });
  dots.forEach((button, index) => {
    button.addEventListener("click", () => {
      rotation.pause();
      navigation.show(index);
    });
  });
  carousel.addEventListener("mouseenter", rotation.pause);
  carousel.addEventListener("focusin", rotation.pause);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) rotation.pause();
  });
  motion.addEventListener("change", () => {
    rotation.pause();
    navigation.settle();
  });
  initSwipe(viewport, rotation.pause, navigation.move);
  controls.hidden = false;
  rotation.setPlaying(
    !motion.matches &&
      !document.hidden &&
      !carousel.matches(":hover") &&
      !carousel.contains(document.activeElement),
  );
}

function initMediaViewer() {
  const thumbnail = document.getElementById(window.location.hash.slice(1));
  if (thumbnail?.matches(".app-media-link"))
    thumbnail.focus({ preventScroll: true });
  const viewer = document.querySelector(".app-media-viewer");
  if (!viewer) return;
  const shortcuts = new Map([
    ["ArrowLeft", '[rel="prev"]'],
    ["ArrowRight", '[rel="next"]'],
    ["Escape", ".app-media-return"],
  ]);
  document.addEventListener("keydown", (event) => {
    if (event.defaultPrevented || hasModifiers(event)) return;
    if (
      event.target instanceof Element &&
      event.target.closest(
        "input, textarea, select, [contenteditable], [role='textbox']",
      )
    )
      return;
    const selector = shortcuts.get(event.key);
    const link = selector
      ? viewer.querySelector<HTMLAnchorElement>(selector)
      : null;
    if (!link) return;
    event.preventDefault();
    window.location.assign(link.href);
  });
}

function initExamFilters() {
  const filters =
    document.querySelector<HTMLDetailsElement>(".app-exams-filters");
  if (filters) {
    filters.open =
      window.matchMedia("(min-width: 48.0625em)").matches ||
      filters.dataset.active === "true";
  }
  const form = document.querySelector("#exams-search");
  form?.addEventListener("change", () => {
    const count = form.querySelectorAll(
      '[name="degree"]:checked, [name="program"]:checked, [name="version"]:checked',
    ).length;
    requireElement("#filter-label").textContent = count
      ? `Filter (${count})`
      : "Filter";
  });
}

function initExamSearchFeedback() {
  document.addEventListener("htmx:beforeRequest", ({ detail }) => {
    if (detail.target?.id !== "exams-results") return;
    requireElement("#search-error").hidden = true;
    detail.target.setAttribute("aria-busy", "true");
  });
  document.addEventListener("htmx:afterRequest", ({ detail }) => {
    if (detail.target?.id !== "exams-results") return;
    detail.target.removeAttribute("aria-busy");
    if (detail.failed) requireElement("#search-error").hidden = false;
  });
  document.addEventListener("htmx:afterSwap", ({ detail }) => {
    if (detail.target?.id !== "exams-results") return;
    document.querySelector<HTMLElement>("#results-heading")?.focus();
    const title =
      document.querySelector<HTMLElement>("#exams-results")?.dataset
        .documentTitle;
    if (title) document.title = title;
  });
}

function initSessionWarning() {
  const candidate =
    document.querySelector<HTMLDialogElement>("#session-warning");
  if (!candidate || typeof candidate.showModal !== "function") return;
  const dialog = candidate;
  const stay = requireElement<HTMLButtonElement>("#session-continue", dialog);
  const signIn = requireElement<HTMLAnchorElement>("#session-sign-in", dialog);
  const countdown = requireElement("#session-countdown", dialog);
  const description = requireElement("#session-warning-description", dialog);
  const error = requireElement("#session-warning-error", dialog);
  let deadline = Number(dialog.dataset.deadline);
  let absolute = Number(dialog.dataset.absolute);
  let offset = Number(dialog.dataset.now) - Date.now();
  let previousFocus: HTMLElement | null = null;
  let pending = false;
  let active = false;
  let lastExtension = Date.now();
  let lastAnnouncement = "";

  function closeWarning() {
    if (!dialog?.open) return;
    dialog.close();
    previousFocus?.focus();
    lastAnnouncement = "";
  }

  async function sync(extend = false) {
    if (pending) return;
    pending = true;
    stay.disabled = true;
    try {
      const response = await fetch(extend ? "/session/extend" : "/session", {
        method: extend ? "POST" : "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: extend
          ? { "Content-Type": "application/x-www-form-urlencoded" }
          : {},
        body: extend
          ? new URLSearchParams({ csrf: dialog?.dataset.csrf ?? "" })
          : undefined,
      });
      if (response.status === 401 || response.status === 403) {
        window.location.replace("/session/expired");
        return;
      }
      if (!response.ok) throw new Error("Session status unavailable");
      const state: unknown = await response.json();
      if (
        typeof state !== "object" ||
        state === null ||
        !("deadline" in state) ||
        typeof state.deadline !== "number" ||
        !("absolute" in state) ||
        typeof state.absolute !== "number" ||
        !("now" in state) ||
        typeof state.now !== "number" ||
        !Number.isFinite(state.deadline) ||
        !Number.isFinite(state.absolute) ||
        !Number.isFinite(state.now)
      )
        throw new Error("Invalid session status");
      deadline = state.deadline;
      absolute = state.absolute;
      offset = state.now - Date.now();
      error.hidden = true;
      if (extend) {
        lastExtension = Date.now();
        active = false;
      }
      if (deadline - (Date.now() + offset) > 120_000) closeWarning();
    } catch {
      if (extend || dialog?.open) error.hidden = false;
    } finally {
      pending = false;
      stay.disabled = false;
    }
  }

  async function continueSession() {
    if (absolute - (Date.now() + offset) <= 120_000) {
      window.location.assign("/login");
      return;
    }
    await sync(true);
  }

  function tick() {
    const now = Date.now() + offset;
    const remaining = deadline - now;
    if (remaining <= 0) {
      void sync(); // Another tab may have extended the server-side deadline.
      return;
    }
    if (remaining > 120_000) return;
    const atLimit = absolute - now <= 120_000;
    stay.hidden = atLimit;
    signIn.hidden = !atLimit;
    description.textContent = atLimit
      ? "Die maximale Sitzungsdauer ist bald erreicht. Melde dich erneut an, um fortzufahren. Nicht gespeicherte Eingaben können dabei verloren gehen."
      : "Ohne weitere Aktivität wirst du automatisch abgemeldet. Nicht gespeicherte Eingaben können dabei verloren gehen.";
    const seconds = Math.ceil(remaining / 1000);
    const message =
      seconds > 60
        ? `Verbleibende Zeit: ${Math.ceil(seconds / 60)} Minuten.`
        : `Verbleibende Zeit: ${Math.ceil(seconds / 20) * 20} Sekunden.`;
    if (!dialog?.open) {
      previousFocus =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      dialog?.showModal();
      requireElement("h2", dialog).focus();
    }
    if (message !== lastAnnouncement) {
      countdown.textContent = message;
      lastAnnouncement = message;
    }
  }

  stay.addEventListener("click", () => void continueSession());
  dialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    void continueSession();
  });
  dialog.addEventListener("keydown", (event) => wrapDialogFocus(dialog, event));
  const markActive = (event: Event) => {
    if (event.isTrusted && !dialog.open && !document.hidden) active = true;
  };
  for (const name of ["keydown", "pointerdown", "input", "scroll"]) {
    document.addEventListener(name, markActive, {
      passive: true,
      capture: true,
    });
  }
  // Rich-text editors use same-origin iframes; typing there is activity too.
  document.addEventListener(
    "load",
    (event) => {
      const frame = event.target;
      if (!(frame instanceof HTMLIFrameElement)) return;
      try {
        for (const name of ["keydown", "pointerdown", "input"]) {
          frame.contentDocument?.addEventListener(name, markActive, {
            passive: true,
          });
        }
      } catch {
        /* Cross-origin frames are not part of the editing interface. */
      }
    },
    true,
  );
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) void sync();
  });
  window.addEventListener("pageshow", () => void sync());
  window.setInterval(() => {
    if (active && !dialog.open && Date.now() - lastExtension >= 60_000) {
      void sync(true);
    } else {
      void sync();
    }
  }, 15_000);
  window.setInterval(tick, 1000);
  tick();
}

function initPageFeedback() {
  const section = document.querySelector<HTMLElement>(".app-page-feedback");
  if (!section) return;
  const prompt = requireElement(".app-page-feedback__prompt", section);
  const panel = requireElement(".app-page-feedback__panel", section);
  let busy = false;
  let lastButton: HTMLButtonElement | null = null;
  const show = async (response: Response) => {
    const doc = new DOMParser().parseFromString(
      await response.text(),
      "text/html",
    );
    const result = doc.querySelector<HTMLElement>("[data-feedback-result]");
    if (!result) throw new Error("Feedback response unavailable");
    const thanks = result.querySelector<HTMLElement>("[data-feedback-thanks]");
    if (thanks) {
      const message = document.createElement("p");
      message.className = "app-page-feedback__thanks";
      message.setAttribute("role", "status");
      message.tabIndex = -1;
      message.textContent = thanks.textContent;
      prompt.replaceChildren(message);
      prompt.hidden = false;
      panel.hidden = true;
      message.focus();
      return;
    }
    panel.replaceChildren(document.importNode(result, true));
    prompt.hidden = true;
    panel.hidden = false;
    panel
      .querySelector<HTMLElement>(".govuk-error-summary, h2, textarea")
      ?.focus();
  };
  section
    .querySelectorAll<HTMLFormElement>("[data-page-feedback]")
    .forEach((form) => {
      form.addEventListener("submit", async (event) => {
        const button = (event as SubmitEvent)
          .submitter as HTMLButtonElement | null;
        if (!button) return;
        event.preventDefault();
        if (busy) return;
        lastButton = button;
        busy = true;
        const params = new URLSearchParams({
          path: window.location.pathname,
          value: button.value,
        });
        const target = `/feedback?${params}`;
        try {
          const response = await fetch(target);
          if (!response.ok) throw new Error("Feedback form unavailable");
          if (button.value === "problem") {
            await show(response);
          } else {
            if (button.value === "no") await show(response.clone());
            const doc = new DOMParser().parseFromString(
              await response.text(),
              "text/html",
            );
            const token =
              doc.querySelector<HTMLInputElement>('input[name="csrf"]')?.value;
            if (!token) throw new Error("Missing feedback token");
            params.set("csrf", token);
            const sent = await fetch("/feedback", {
              method: "POST",
              body: params,
            });
            if (!sent.ok) throw new Error("Feedback delivery failed");
            await show(sent);
          }
        } catch {
          const container = panel.hidden ? prompt : panel;
          let error = container.querySelector<HTMLElement>(
            "[data-feedback-error]",
          );
          if (!error) {
            error = document.createElement("p");
            error.dataset.feedbackError = "";
            error.className = "govuk-error-message";
            error.setAttribute("role", "alert");
            container.append(error);
          }
          error.textContent =
            "Deine Rückmeldung konnte nicht gesendet werden. Versuche es später erneut.";
        } finally {
          busy = false;
        }
      });
    });
  panel.addEventListener("click", (event) => {
    if (
      !(event.target instanceof Element) ||
      !event.target.closest("[data-feedback-cancel]")
    )
      return;
    event.preventDefault();
    panel.hidden = true;
    prompt.hidden = false;
    lastButton?.focus();
  });
  panel.addEventListener("submit", async (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    event.preventDefault();
    if (busy) return;
    busy = true;
    const button = form.querySelector<HTMLButtonElement>(
      'button[type="submit"]',
    );
    if (button) button.disabled = true;
    try {
      const data = new URLSearchParams();
      new FormData(form).forEach((value, key) => {
        if (typeof value === "string") data.set(key, value);
      });
      const response = await fetch(form.action, { method: "POST", body: data });
      if (!response.ok && response.status !== 422)
        throw new Error("Feedback delivery failed");
      await show(response);
    } catch {
      let error = panel.querySelector<HTMLElement>("[data-feedback-error]");
      if (!error) {
        error = document.createElement("p");
        error.dataset.feedbackError = "";
        error.className = "govuk-error-message";
        error.setAttribute("role", "alert");
        form.prepend(error);
      }
      error.textContent =
        "Deine Rückmeldung konnte nicht gesendet werden. Versuche es später erneut.";
    } finally {
      busy = false;
      if (button) button.disabled = false;
    }
  });
}

function initSite() {
  initPageFeedback();
  initFrontend(document);
  initCookies();
  document.querySelectorAll<HTMLFormElement>("form.app-vote").forEach(initVote);
  document.querySelectorAll<HTMLElement>("[data-dialog]").forEach(initDialog);
  document
    .querySelectorAll<HTMLElement>(".app-event-carousel")
    .forEach(initCarousel);
  initUploadResponses();
  initMediaViewer();
  initExamFilters();
  initExamSearchFeedback();
  initSessionWarning();
}

initSite();
