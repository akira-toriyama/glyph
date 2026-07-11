{
  # glyph — `nix run github:akira-toriyama/glyph` or `nix profile install`.
  #
  # vendorHash pins the vendored go modules; when go.mod/go.sum change, set it
  # back to pkgs.lib.fakeHash, run `nix build`, and paste the hash nix prints
  # ("got: sha256-...").
  description = "gitmoji-driven commit-lint, semver, and release notes for squash-merge repos";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        # Source/nix builds report a "dev" version identified by the commit;
        # tagged releases carry their real version via goreleaser (see
        # .goreleaser.yaml). Deriving the commit here avoids a hardcoded, stale
        # version string.
        version = "dev";
        rev = self.shortRev or self.dirtyShortRev or "unknown";
        v = "github.com/akira-toriyama/glyph/internal/version";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "glyph";
          inherit version;
          src = ./.;
          vendorHash = "sha256-7K17JaXFsjf163g5PXCb5ng2gYdotnZ2IDKk8KFjNj0=";
          ldflags = [
            "-s" "-w"
            "-X ${v}.Version=${version}"
            "-X ${v}.Commit=${rev}"
          ];
          subPackages = [ "cmd/glyph" ];
          meta = with pkgs.lib; {
            description = "gitmoji-driven commit-lint, semver, and release notes for squash-merge repos";
            homepage = "https://github.com/akira-toriyama/glyph";
            license = licenses.mit;
            mainProgram = "glyph";
          };
        };

        apps.default = flake-utils.lib.mkApp {
          drv = self.packages.${system}.default;
          name = "glyph";
        };

        devShells.default = pkgs.mkShell {
          # go (not a pinned go_1_xx): nixpkgs removed EOL go versions; go.mod's
          # floor is satisfied by any current toolchain (GOTOOLCHAIN=local). No
          # git-cliff: glyph replaces it, so its own dev shell never carries it.
          packages = [ pkgs.go pkgs.golangci-lint pkgs.goreleaser pkgs.govulncheck ];
        };
      });
}
