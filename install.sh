#!/bin/sh
# Install metron, and optionally its agent skill.
#
#   curl -fsSL https://raw.githubusercontent.com/yanmxa/metron/main/install.sh | sh
#
# The skill is one document. Every coding assistant reads instructions from a
# different path in a different wrapper, so this writes the same body wherever
# the one you use will look for it.
set -eu

REPO="yanmxa/metron"
RAW="https://raw.githubusercontent.com/$REPO/main"
BIN_DIR="${METRON_BIN_DIR:-}"
AGENT=""
DO_BIN=1
DO_SKILL=0
VERSION="latest"

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'USAGE'
metron installer

  install.sh [options]

Options:
  --skill [AGENT]   also install the agent skill (see below)
  --skill-only      install only the skill, not the binary
  --agent AGENT     which assistant to write the skill for
  --dir DIR         where to put the binary (default: /usr/local/bin, or ~/.local/bin)
  --version VER     release to install (default: latest)
  -h, --help        this text

Agents:
  claude      .claude/skills/metron/SKILL.md
  cursor      .cursor/rules/metron.mdc
  copilot     .github/copilot-instructions.md
  windsurf    .windsurf/rules/metron.md
  agents      AGENTS.md          (Codex CLI, Amp, and anything else reading it)
  all         every one of the above
  auto        whichever are already present  (default)

Examples:
  install.sh                          binary only
  install.sh --skill                  binary, plus the skill for whatever is here
  install.sh --skill-only --agent all skill for every assistant, no binary
USAGE
}

while [ $# -gt 0 ]; do
	case "$1" in
		--skill)      DO_SKILL=1; case "${2:-}" in -*|"") ;; *) AGENT="$2"; shift;; esac ;;
		--skill-only) DO_SKILL=1; DO_BIN=0 ;;
		--agent)      DO_SKILL=1; AGENT="${2:?--agent needs a value}"; shift ;;
		--dir)        BIN_DIR="${2:?--dir needs a value}"; shift ;;
		--version)    VERSION="${2:?--version needs a value}"; shift ;;
		-h|--help)    usage; exit 0 ;;
		*)            die "unknown option: $1 (try --help)" ;;
	esac
	shift
done

# --- binary ----------------------------------------------------------------

detect_platform() {
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	arch=$(uname -m)
	case "$arch" in
		x86_64|amd64) arch=amd64 ;;
		arm64|aarch64) arch=arm64 ;;
		*) die "unsupported architecture: $arch. Build from source: go install github.com/$REPO/cmd/metron@latest" ;;
	esac
	case "$os" in
		linux|darwin) ;;
		*) die "unsupported OS: $os. Build from source: go install github.com/$REPO/cmd/metron@latest" ;;
	esac
	printf '%s_%s' "$os" "$arch"
}

choose_bin_dir() {
	[ -n "$BIN_DIR" ] && { printf '%s' "$BIN_DIR"; return; }
	if [ -w /usr/local/bin ] 2>/dev/null; then
		printf '/usr/local/bin'
	else
		printf '%s/.local/bin' "$HOME"
	fi
}

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		die "need curl or wget"
	fi
}

install_binary() {
	platform=$(detect_platform)
	dir=$(choose_bin_dir)
	mkdir -p "$dir"

	if [ "$VERSION" = latest ]; then
		url="https://github.com/$REPO/releases/latest/download/metron_${platform}.tar.gz"
	else
		url="https://github.com/$REPO/releases/download/${VERSION}/metron_${platform}.tar.gz"
	fi

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	say "downloading metron ($platform)..."
	if ! fetch "$url" "$tmp/metron.tar.gz" 2>/dev/null; then
		warn "no release binary for $platform ($VERSION)."
		if command -v go >/dev/null 2>&1; then
			say "building from source instead..."
			GOBIN="$dir" go install "github.com/$REPO/cmd/metron@latest"
			say "installed $dir/metron"
			return
		fi
		die "install Go and retry, or download a release from https://github.com/$REPO/releases"
	fi

	tar -xzf "$tmp/metron.tar.gz" -C "$tmp"
	[ -f "$tmp/metron" ] || die "archive did not contain a metron binary"
	mv "$tmp/metron" "$dir/metron"
	chmod +x "$dir/metron"
	say "installed $dir/metron"

	case ":$PATH:" in
		*":$dir:"*) ;;
		*) warn "note: $dir is not on your PATH" ;;
	esac
}

# --- skill -----------------------------------------------------------------

DESC="Measure Go code with metron - mutation score, cognitive complexity, and graph-level redundancy - then act on the specific change each finding names. Use after writing or changing Go code, when reviewing code health, or when asked whether tests actually hold the code up."

body() {
	if [ -f agent/metron.md ]; then
		cat agent/metron.md
	else
		fetch "$RAW/agent/metron.md" /dev/stdout
	fi
}

write_file() {
	path="$1"
	mkdir -p "$(dirname "$path")"
	cat > "$path"
	say "  wrote $path"
}

install_claude() {
	{ printf -- '---\nname: metron\ndescription: %s\n---\n\n' "$DESC"; body; } |
		write_file .claude/skills/metron/SKILL.md
}

install_cursor() {
	{ printf -- '---\ndescription: %s\nglobs: ["**/*.go"]\nalwaysApply: false\n---\n\n' "$DESC"; body; } |
		write_file .cursor/rules/metron.mdc
}

install_windsurf() {
	{ printf -- '---\ntrigger: model_decision\ndescription: %s\n---\n\n' "$DESC"; body; } |
		write_file .windsurf/rules/metron.md
}

install_copilot() {
	body | write_file .github/copilot-instructions.md
}

# AGENTS.md is shared with whatever else the project put there, so replace only
# metron's own section rather than the file.
install_agents() {
	marker_start="<!-- metron:start -->"
	marker_end="<!-- metron:end -->"
	tmpf=$(mktemp)

	if [ -f AGENTS.md ]; then
		awk -v s="$marker_start" -v e="$marker_end" '
			$0 == s { skip = 1 } !skip { print } $0 == e { skip = 0 }
		' AGENTS.md > "$tmpf"
		printf '\n' >> "$tmpf"
	fi

	{ printf '%s\n' "$marker_start"; body; printf '%s\n' "$marker_end"; } >> "$tmpf"
	mv "$tmpf" AGENTS.md
	say "  wrote AGENTS.md (metron section)"
}

detect_agents() {
	found=""
	[ -d .claude ]   && found="$found claude"
	[ -d .cursor ]   && found="$found cursor"
	[ -d .windsurf ] && found="$found windsurf"
	[ -f AGENTS.md ] && found="$found agents"
	[ -f .github/copilot-instructions.md ] && found="$found copilot"
	printf '%s' "$found"
}

install_skill() {
	targets="$AGENT"
	if [ -z "$targets" ] || [ "$targets" = auto ]; then
		targets=$(detect_agents)
		if [ -z "$targets" ]; then
			warn "no assistant detected here; writing AGENTS.md, which most of them read."
			warn "use --agent claude|cursor|copilot|windsurf|all to choose."
			targets="agents"
		else
			say "detected:$targets"
		fi
	fi
	[ "$targets" = all ] && targets="claude cursor windsurf copilot agents"

	say "installing skill..."
	for a in $targets; do
		case "$a" in
			claude)   install_claude ;;
			cursor)   install_cursor ;;
			windsurf) install_windsurf ;;
			copilot)  install_copilot ;;
			agents)   install_agents ;;
			*) die "unknown agent: $a (try --help)" ;;
		esac
	done
}

# --- run -------------------------------------------------------------------

[ "$DO_BIN" = 1 ] && install_binary
[ "$DO_SKILL" = 1 ] && install_skill

say ""
say "next: metron --since main --axes all"
