{
  description = "Apple Music as your Discord presence";

  inputs = {
    nixpkgs.url = "nixpkgs/nixos-unstable";
  };

  outputs = {
    self,
    nixpkgs,
    ...
  }: let
    version = builtins.substring 0 8 self.lastModifiedDate;

    systems = [
      "x86_64-linux"
      "aarch64-linux"
      "x86_64-darwin"
      "aarch64-darwin"
    ];

    packageFn = pkgs: let
      inherit (pkgs) lib;
    in {
      discord-applemusic-rich-presence = pkgs.buildGoModule rec {
        pname = "discord-applemusic-rich-presence";
        inherit version;

        src = builtins.path {
          name = "${pname}-src";
          path = lib.cleanSource ./.;
        };

        vendorHash = "sha256-sJJ5qJUwLUN8uXPcLwslJmn/iNe6Ci2dB0eo9hiRdwE=";

        meta = {
          description = "Apple Music as your Discord presence";
          homepage = "https://github.com/ryanccn/${pname}";
          license = lib.licenses.mit;
          maintainers = with lib.maintainers; [ryanccn];
        };
      };
    };

    forAllSystems = nixpkgs.lib.genAttrs systems;
    nixpkgsFor = forAllSystems (system: import nixpkgs {inherit system;});
  in {
    devShells = forAllSystems (s: let
      pkgs = nixpkgsFor.${s};
      inherit (pkgs) mkShell;
    in {
      default = mkShell {
        packages = [pkgs.go];
      };
    });

    packages = forAllSystems (s: let
      p = packageFn nixpkgsFor.${s};
    in
      p // {default = p.discord-applemusic-rich-presence;});

    overlays.default = _: prev: (packageFn prev);
  };
}
