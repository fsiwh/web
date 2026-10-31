import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const options = new Set(process.argv.slice(2));
if ([...options].some((option) => !["--assets", "--go", "--check"].includes(option))) {
  throw new Error("Expected --assets and/or --go, with optional --check");
}
if (!options.has("--assets") && !options.has("--go")) {
  throw new Error("Select --assets or --go");
}
const original = await readFile("flake.nix", "utf8");
let flake = original;

if (options.has("--assets")) {
  const variables = new Map(
    [...flake.matchAll(/\b(\w+(?:Version|Revision)) = "([^"]+)";/g)].map(
      ([, name, value]) => [name, value],
    ),
  );
  const blocks = [...flake.matchAll(/pkgs\.fetchurl \{[\s\S]*?\n\s*\};/g)];
  if (blocks.length !== 7) {
    throw new Error("Expected all seven asset downloads, including the GOV.UK licence");
  }
  for (const [block] of blocks) {
    const rawURL = block.match(/url = "([^"]+)";/)?.[1];
    const oldHash = block.match(/hash = "([^"]+)";/)?.[1];
    if (!rawURL || !oldHash) throw new Error("Asset download has no URL or hash");
    const url = rawURL.replace(/\$\{(\w+)\}/g, (_, name) => {
      if (!variables.has(name)) throw new Error(`Unknown asset variable ${name}`);
      return variables.get(name);
    });
    const response = await fetch(url, { signal: AbortSignal.timeout(120_000) });
    if (!response.ok) throw new Error(`Download failed: ${response.status} ${url}`);
    const hash = `sha256-${createHash("sha256")
      .update(Buffer.from(await response.arrayBuffer()))
      .digest("base64")}`;
    flake = flake.replace(block, block.replace(`hash = "${oldHash}"`, `hash = "${hash}"`));
    console.log(`Verified asset hash: ${url}`);
  }
}

if (options.has("--go")) {
  const goMod = await readFile("go.mod", "utf8");
  const current = goMod.match(/^go (\d+\.\d+)\.\d+$/m)?.[1];
  if (!current) throw new Error("Missing Go toolchain version");
  // Resolve only the locked input, without evaluating or copying application data.
  const version = execFileSync(
    "nix",
    [
      "--extra-experimental-features", "nix-command flakes",
      "eval", "--raw", "--inputs-from", ".", `nixpkgs#go_${current.replace(".", "_")}.version`,
    ],
    { encoding: "utf8" },
  ).trim();
  if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error("Invalid locked Go version");
  const updatedGoMod = goMod.replace(/^go [\d.]+$/m, `go ${version}`);
  const toolsGoMod = await readFile("tools/go.mod", "utf8");
  const updatedToolsGoMod = toolsGoMod.replace(/^go [\d.]+$/m, `go ${version}`);
  const dockerfile = await readFile("Dockerfile", "utf8");
  const image = dockerfile.match(/golang:([^\s]+)-alpine@(sha256:[a-f0-9]{64})/);
  if (!image) throw new Error("Missing digest-pinned Go build image");
  let updatedDockerfile = dockerfile;
  if (image[1] !== version) {
    const auth = await fetch("https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/golang:pull");
    if (!auth.ok) throw new Error("Cannot authenticate to the public Docker registry");
    const { token } = await auth.json();
    const response = await fetch(`https://registry-1.docker.io/v2/library/golang/manifests/${version}-alpine`, {
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json",
      },
    });
    const digest = response.headers.get("docker-content-digest");
    if (!response.ok || !/^sha256:[a-f0-9]{64}$/.test(digest ?? "")) {
      throw new Error(`No published Go build image for ${version}`);
    }
    updatedDockerfile = dockerfile.replace(image[0], `golang:${version}-alpine@${digest}`);
  }
  if (options.has("--check") && (goMod !== updatedGoMod || toolsGoMod !== updatedToolsGoMod || dockerfile !== updatedDockerfile)) {
    throw new Error("Go module, Nix and Docker toolchain versions need synchronization");
  }
  if (!options.has("--check")) {
    if (goMod !== updatedGoMod) await writeFile("go.mod", updatedGoMod);
    if (toolsGoMod !== updatedToolsGoMod) await writeFile("tools/go.mod", updatedToolsGoMod);
    if (dockerfile !== updatedDockerfile) await writeFile("Dockerfile", updatedDockerfile);
  }
  // SQLite's transpiled code is validated against an exact libc version.
  const sqlite = JSON.parse(execFileSync("go", ["list", "-m", "-json", "modernc.org/sqlite"], { encoding: "utf8" }));
  const sqliteGoMod = JSON.parse(execFileSync("go", ["mod", "edit", "-json", sqlite.GoMod], { encoding: "utf8" }));
  const expectedLibc = sqliteGoMod.Require.find(({ Path }) => Path === "modernc.org/libc")?.Version;
  const actualLibc = execFileSync("go", ["list", "-m", "-f", "{{.Version}}", "modernc.org/libc"], { encoding: "utf8" }).trim();
  if (!expectedLibc || actualLibc !== expectedLibc) {
    throw new Error(`SQLite ${sqlite.Version} requires libc ${expectedLibc}, selected ${actualLibc}`);
  }
  console.log(`Verified SQLite/libc compatibility: ${sqlite.Version} / ${actualLibc}`);
  if (!options.has("--check")) {
    execFileSync("go", ["mod", "tidy", "-C", "tools"], { stdio: "inherit" });
    execFileSync("go", ["generate", "./internal/database"], { stdio: "inherit" });
  }
  const directory = await mkdtemp(join(tmpdir(), "web-vendor-"));
  try {
    const vendor = join(directory, "vendor");
    execFileSync("go", ["mod", "vendor", "-o", vendor], { stdio: "inherit" });
    const hash = execFileSync(
      "nix",
      ["--extra-experimental-features", "nix-command", "hash", "path", vendor],
      { encoding: "utf8" },
    ).trim();
    if (!/^sha256-[A-Za-z0-9+/]{43}=$/.test(hash)) {
      throw new Error("Nix returned an invalid vendor hash");
    }
    if (!/vendorHash = "[^"]+";/.test(flake)) throw new Error("Missing Go vendor hash");
    flake = flake.replace(/vendorHash = "[^"]+";/, `vendorHash = "${hash}";`);
    console.log(`Verified Go vendor hash: ${hash}`);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

if (flake !== original) {
  if (options.has("--check")) throw new Error("Dependency hashes need regeneration");
  await writeFile("flake.nix", flake);
  console.log("Updated dependency hashes in flake.nix");
}
