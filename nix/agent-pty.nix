# A function of pkgs, so a consumer builds agent-pty with its own pinned
# nixpkgs. vendorHash pins pst, the only dependency: refresh it whenever
# go.mod or go.sum change, or nix keeps building the old download.
{ pkgs }:
pkgs.buildGoModule {
  pname = "agent-pty";
  version = "0.1.0";
  src = pkgs.lib.cleanSource ../.;
  vendorHash = "sha256-FqSp/oqjFU9hJDzHUgpP6+5pg45P3UQiK94TzWdGXpk=";
  subPackages = [ "cmd/agent-pty" ];
  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
  ];
  # The end-to-end tests run a fake agent on a pty, which the build sandbox
  # does not provide; run them with `go test ./...`.
  doCheck = false;

  meta = {
    description = "OSC 7501 relay that gives coding agents in tmux the terminal status protocol";
    homepage = "https://github.com/sespiros/agent-pty";
    license = pkgs.lib.licenses.gpl3Only;
    mainProgram = "agent-pty";
  };
}
