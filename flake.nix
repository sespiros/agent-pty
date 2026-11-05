{
  description = "OSC 7501 relay that gives coding agents in tmux the terminal status protocol";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      # A function of pkgs, so a consumer builds agent-pty with its own pinned
      # nixpkgs.
      lib.agentPty = import ./nix/agent-pty.nix;

      packages = forAll (pkgs: {
        default = self.lib.agentPty { inherit pkgs; };
      });

      checks = forAll (pkgs: {
        agent-pty = self.lib.agentPty { inherit pkgs; };
      });

      formatter = forAll (pkgs: pkgs.nixfmt);
    };
}
