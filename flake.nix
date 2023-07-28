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
      "x86_64-darwin"
      "aarch64-darwin"
    ];

    packageFn = pkgs: let
      inherit (pkgs) lib stdenv;
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
          platforms = lib.platforms.darwin;
          broken = stdenv.isLinux;
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

    homeManagerModules = {
      default = {
        config,
        lib,
        pkgs,
        ...
      }: let
        cfg = config.services.discord-applemusic-rich-presence;
        selfPkgs = packageFn pkgs;
        inherit (lib) mkEnableOption mkPackageOption mkOption mkMerge mkIf types;
      in {
        options.services.discord-applemusic-rich-presence = {
          enable = mkEnableOption "discord-applemusic-rich-presence";
          package = mkPackageOption selfPkgs "discord-applemusic-rich-presence" {};

          logFile = mkOption {
            type = types.nullOr types.path;
            default = null;
            example = ''
              ${config.xdg.cacheHome}/discord-applemusic-rich-presence.log
            '';
          };
        };

        config = mkMerge [
          (mkIf cfg.enable {
            launchd.agents.discord-applemusic-rich-presence = {
              enable = true;

              config = {
                ProgramArguments = ["${cfg.package}/bin/discord-applemusic-rich-presence"];
                KeepAlive = true;
                RunAtLoad = true;

                StandardOutPath = cfg.logFile;
                StandardErrorPath = cfg.logFile;
              };
            };
          })
        ];
      };
    };
  };
}
