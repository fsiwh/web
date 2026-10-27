{
  description = "Reproducible application, image, and development environment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      forAllSystems = nixpkgs.lib.genAttrs [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          goVersion = builtins.head (builtins.match ".*\ngo ([0-9.]+)\n.*" (builtins.readFile ./go.mod));
          go = pkgs.${"go_" + builtins.replaceStrings [ "." ] [ "_" ] (pkgs.lib.versions.majorMinor goVersion)};
          # renovate: datasource=npm depName=govuk-frontend
          govukVersion = "6.6.0";
          # renovate: datasource=npm depName=htmx.org
          htmxVersion = "2.0.11";
          # renovate: datasource=npm depName=tinymce
          tinymceVersion = "8.10.0";
          # renovate: datasource=npm depName=tinymce-i18n
          languageVersion = "26.10.19";
          # renovate: datasource=git-refs depName=https://github.com/alphagov/govspeak-visual-editor
          iconsRevision = "cf09bfaed71624e789de655283f2a942bc3ca87b";
          # renovate: datasource=git-refs depName=https://github.com/alphagov/govuk-design-system
          communityRevision = "83ef3622ff74d47170d642ea7811e4ea4a440ef9";

          govuk = pkgs.fetchurl {
            url = "https://registry.npmjs.org/govuk-frontend/-/govuk-frontend-${govukVersion}.tgz";
            hash = "sha256-UsTrVU5U2KbkuoAenaXsOluTD9LxF6OWd/C2KW6VR1E=";
          };
          govukLicense = pkgs.fetchurl {
            url = "https://raw.githubusercontent.com/alphagov/govuk-frontend/v${govukVersion}/LICENSE.txt";
            hash = "sha256-xlLzWx8uW4jrSfHoqTXux5tHYagWZqzlduGvcj9Kkjg=";
          };
          htmx = pkgs.fetchurl {
            url = "https://registry.npmjs.org/htmx.org/-/htmx.org-${htmxVersion}.tgz";
            hash = "sha256-FlomVQc+nm5V4wenx06OvNt2r7dwH5wWz9EGoTrdQI4=";
          };
          tinymce = pkgs.fetchurl {
            url = "https://registry.npmjs.org/tinymce/-/tinymce-${tinymceVersion}.tgz";
            hash = "sha256-yCfom0e+9PAuKJLaM19UCSU1n3VGeQWOajDLsEmUba4=";
          };
          language = pkgs.fetchurl {
            url = "https://registry.npmjs.org/tinymce-i18n/-/tinymce-i18n-${languageVersion}.tgz";
            hash = "sha256-+ffkCojl+AHPf5qyxA/jxHeM3ExVKBUbe+1smhHeCXY=";
          };
          icons = pkgs.fetchurl {
            name = "govspeak-visual-editor-${iconsRevision}.tar.gz";
            url = "https://codeload.github.com/alphagov/govspeak-visual-editor/tar.gz/${iconsRevision}";
            hash = "sha256-btqnuSYTvOpGH2pB5XVGrDWy6/SmFHQzFjie/mIWs1w=";
          };
          community = pkgs.fetchurl {
            name = "govuk-design-system-${communityRevision}.tar.gz";
            url = "https://codeload.github.com/alphagov/govuk-design-system/tar.gz/${communityRevision}";
            hash = "sha256-Kqcr2pHN8KcIjX26hUgCNO6o+d+5mF7Ng050t95tb+o=";
          };
          assets = pkgs.stdenvNoCC.mkDerivation {
            pname = "web-browser-assets";
            version = "1";
            dontUnpack = true;
            nativeBuildInputs = [ pkgs.dart-sass pkgs.typescript pkgs.prettier ];

            buildPhase = ''
              runHook preBuild
              for name in govuk htmx tinymce language icons community; do
                mkdir -p "$name"
              done
              tar -xzf ${govuk} -C govuk --strip-components=1
              tar -xzf ${htmx} -C htmx --strip-components=1
              tar -xzf ${tinymce} -C tinymce --strip-components=1
              tar -xzf ${language} -C language --strip-components=1
              tar -xzf ${icons} -C icons --strip-components=1
              tar -xzf ${community} -C community --strip-components=1

              sass --no-source-map --style=compressed --quiet-deps \
                --load-path=govuk/dist ${./view/styles/govuk.scss} govuk.css
              sass --no-source-map --style=compressed ${./view/styles/site.scss} site.css
              sass --no-source-map --style=compressed ${./view/styles/editor.scss} editor.css
              mkdir -p scripts/node_modules
              cp ${./view/site.ts} scripts/site.ts
              cp ${./view/editor.ts} scripts/editor.ts
              cp ${./view/govuk.d.ts} scripts/govuk.d.ts
              ln -s "$PWD/tinymce" scripts/node_modules/tinymce
              prettier --check scripts/*.ts
              tsc --pretty false --strict --target ESNext --module ESNext --moduleResolution bundler \
                --lib ESNext,DOM,DOM.Iterable --rootDir scripts --outDir js scripts/site.ts scripts/editor.ts
              runHook postBuild
            '';

            installPhase = ''
              runHook preInstall
              mkdir -p "$out/fonts" "$out/icons" "$out/community"
              install -m 0644 govuk.css "$out/govuk.css"
              install -m 0644 site.css editor.css "$out/"
              install -m 0644 js/site.js js/editor.js "$out/"
              install -m 0644 govuk/dist/govuk/govuk-frontend.min.js "$out/govuk.js"
              install -m 0644 govuk/dist/govuk/assets/fonts/*.woff2 "$out/fonts/"
              install -m 0644 ${govukLicense} "$out/GOVUK-LICENSE.txt"
              install -m 0644 htmx/dist/htmx.min.js "$out/htmx.js"
              install -m 0644 htmx/LICENSE "$out/HTMX-LICENSE.txt"

              for path in tinymce.min.js themes/silver/theme.min.js \
                icons/default/icons.min.js models/dom/model.min.js \
                skins/content/default/content.min.css plugins/lists/plugin.min.js; do
                mkdir -p "$out/tinymce/$(dirname "$path")"
                install -m 0644 "tinymce/$path" "$out/tinymce/$path"
              done
              mkdir -p "$out/tinymce/skins/ui/oxide" "$out/tinymce/langs8"
              install -m 0644 tinymce/skins/ui/oxide/*.min.css "$out/tinymce/skins/ui/oxide/"
              install -m 0644 language/langs8/de.js "$out/tinymce/langs8/de.js"
              install -m 0644 tinymce/license.md "$out/tinymce/LICENSE.txt"
              install -m 0644 language/LICENSE.md "$out/tinymce/I18N-LICENSE.md"

              for name in bullet-list ordered-list link undo redo; do
                install -m 0644 "icons/lib/icons/$name.svg" "$out/icons/$name.svg"
              done
              install -m 0644 icons/LICENCE "$out/icons/GDS-LICENCE.txt"
              for name in discuss-and-give-feedback.svg share-your-examples-and-research.svg \
                propose-a-change-to-pages.svg research-interview.png catchup-groupshot.png workshop.png; do
                install -m 0644 "community/src/community/images/$name" "$out/community/$name"
              done
              install -m 0644 community/LICENSE "$out/community/GDS-LICENCE.txt"
              runHook postInstall
            '';
          };
          revision = self.rev or self.dirtyRev or "dev";
          web = assert pkgs.lib.assertMsg (go.version == goVersion)
            "The locked Nix Go version must match go.mod; update go.mod and flake.lock together.";
            (pkgs.buildGoModule.override { inherit go; }) {
            pname = "web";
            version = revision;
            src = pkgs.lib.cleanSource ./.;
            vendorHash = "sha256-PwnDhaeUafCeGpBTShN1VnLR76kGML3k3YWz22A0lxk=";
            overrideModAttrs = _: {
              name = "web-go-modules";
              preBuild = "";
            };
            subPackages = [ "." ];
            env.CGO_ENABLED = "0";
            preBuild = ''
              cp -R ${assets}/. view/assets/
            '';
            ldflags = [ "-s" "-w" "-X main.Version=${revision}" "-X main.Revision=${revision}" ];
            doCheck = false;
          };
        in
        {
          inherit assets web;
          default = web;
        } // pkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          image = pkgs.dockerTools.buildLayeredImage {
            name = "ghcr.io/fsiwh/web";
            tag = "${revision}";
            contents = [ web pkgs.cacert pkgs.tzdata ];
            extraCommands = ''
              mkdir -p data tmp
              chmod 1777 tmp
            '';
            fakeRootCommands = ''
              chown 1001:1001 data
            '';
            config = {
              User = "1001:1001";
              WorkingDir = "/data";
              Entrypoint = [ "${web}/bin/web" ];
              Cmd = [ "-addr" "0.0.0.0:8080" "-database" "/data/sqlite.db" ];
              Env = [ "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt" "TZDIR=${pkgs.tzdata}/share/zoneinfo" ];
              ExposedPorts."8080/tcp" = { };
              Labels."org.opencontainers.image.revision" = revision;
              Healthcheck = {
                Test = [ "CMD" "${web}/bin/web" "-check-health" "http://127.0.0.1:8080/healthz" ];
                Interval = 30000000000;
                Timeout = 5000000000;
                StartPeriod = 5000000000;
                Retries = 3;
              };
            };
          };
        });

      apps = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          vendor = pkgs.writeShellApplication {
            name = "vendor-assets";
            runtimeInputs = [ pkgs.coreutils ];
            text = ''
              root="''${1:-$PWD}"
              if [ ! -f "$root/view/styles/govuk.scss" ]; then
                echo "Run from the repository root or pass its path." >&2
                exit 1
              fi
              destination="$root/view/assets"
              mkdir -p "$destination"
              rm -rf "$destination/fonts" "$destination/icons" "$destination/community" "$destination/tinymce"
              cp -R --no-preserve=mode,ownership ${self.packages.${system}.assets}/. "$destination/"
            '';
          };
        in
        {
          vendor = {
            type = "app";
            program = "${vendor}/bin/vendor-assets";
            meta.description = "Prepare embedded browser assets";
          };
        });

      checks = forAllSystems (system: {
        assets = self.packages.${system}.assets;
      });

      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          goVersion = builtins.head (builtins.match ".*\ngo ([0-9.]+)\n.*" (builtins.readFile ./go.mod));
          go = pkgs.${"go_" + builtins.replaceStrings [ "." ] [ "_" ] (pkgs.lib.versions.majorMinor goVersion)};
        in
        {
          default = assert pkgs.lib.assertMsg (go.version == goVersion)
            "The locked Nix Go version must match go.mod; update go.mod and flake.lock together.";
            pkgs.mkShellNoCC {
              packages = [ go pkgs.prettier ];
              GOTOOLCHAIN = "local";
              shellHook = ''
                ${self.apps.${system}.vendor.program} "$PWD"
                export ACCOUNT_DOMAINS="''${ACCOUNT_DOMAINS-studmail.w-hs.de,fsv-wh.de,fsi-wh.de}"
                export REGISTRATION_DOMAINS="''${REGISTRATION_DOMAINS-studmail.w-hs.de}"
              '';
            };
        });
    };
}
