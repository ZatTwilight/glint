package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const glintPiExtensionMarker = "glint-pi-session-extension-marker v1"
const glintPiExtensionFilename = "glint-session.ts"

func runHooks(args []string) error {
	if len(args) < 1 || args[0] == "-h" || args[0] == "--help" {
		return fmt.Errorf("usage: glint hooks <install|uninstall> pi [--yes] [--bin <path>]")
	}
	switch args[0] {
	case "install":
		return runHooksInstall(args[1:])
	case "uninstall":
		return runHooksUninstall(args[1:])
	default:
		return fmt.Errorf("unknown hooks command %q; usage: glint hooks <install|uninstall> pi", args[0])
	}
}

func runHooksInstall(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: glint hooks install pi [--yes] [--bin <path>]")
	}
	agentName := strings.ToLower(args[0])
	flags, err := parseHookFlags(args[1:])
	if err != nil {
		return err
	}
	if agentName != "pi" {
		return fmt.Errorf("unsupported agent %q; currently supported: pi", agentName)
	}
	bin, err := resolveGlintBinary(flags["bin"])
	if err != nil {
		return err
	}
	return installPiHook(bin, flags["yes"] == "true" || flags["y"] == "true")
}

func runHooksUninstall(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: glint hooks uninstall pi [--yes]")
	}
	agentName := strings.ToLower(args[0])
	flags, err := parseHookFlags(args[1:])
	if err != nil {
		return err
	}
	if agentName != "pi" {
		return fmt.Errorf("unsupported agent %q; currently supported: pi", agentName)
	}
	return uninstallPiHook(flags["yes"] == "true" || flags["y"] == "true")
}

func installPiHook(glintBin string, yes bool) error {
	path, err := piExtensionPath()
	if err != nil {
		return err
	}
	source := piExtensionSource(glintBin)
	existing, _ := os.ReadFile(path)
	if string(existing) == source {
		fmt.Printf("Pi hook already up to date at %s\n", path)
		return nil
	}
	if len(existing) > 0 && !strings.Contains(string(existing), glintPiExtensionMarker) {
		return fmt.Errorf("%s exists and is not a Glint extension; refusing to overwrite", path)
	}
	if !yes {
		fmt.Printf("Will write Pi extension to %s\n\n", path)
		fmt.Println(source)
		fmt.Print("\nProceed? [y/N] ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if !strings.HasPrefix(strings.ToLower(answer), "y") {
			fmt.Println("Aborted.")
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		return err
	}
	fmt.Printf("Pi hook installed at %s\n", path)
	fmt.Println("Restart Pi or run /reload in Pi to load it.")
	return nil
}

func uninstallPiHook(yes bool) error {
	path, err := piExtensionPath()
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		fmt.Printf("No Pi hook found at %s\n", path)
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(existing), glintPiExtensionMarker) {
		return fmt.Errorf("%s is not a Glint extension; refusing to remove", path)
	}
	if !yes {
		fmt.Printf("Remove Pi extension %s? [y/N] ", path)
		var answer string
		_, _ = fmt.Scanln(&answer)
		if !strings.HasPrefix(strings.ToLower(answer), "y") {
			fmt.Println("Aborted.")
			return nil
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	fmt.Printf("Removed Pi hook from %s\n", path)
	fmt.Println("Restart Pi or run /reload in Pi to unload it.")
	return nil
}

func piExtensionPath() (string, error) {
	base := os.Getenv("PI_CODING_AGENT_DIR")
	if strings.TrimSpace(base) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(base, "extensions", glintPiExtensionFilename), nil
}

func resolveGlintBinary(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(expandPath(explicit))
	}
	if env := os.Getenv("GLINT_BIN"); strings.TrimSpace(env) != "" {
		return filepath.Abs(expandPath(env))
	}
	if path, err := exec.LookPath("glint"); err == nil {
		return filepath.Abs(path)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	if strings.Contains(exe, string(filepath.Separator)+"go-build") {
		return "", fmt.Errorf("cannot install from go run's temporary binary; run `go install ./cmd/glint` or pass --bin /path/to/glint")
	}
	return exe, nil
}

func expandPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func piExtensionSource(glintBin string) string {
	return fmt.Sprintf(`// %s
// Bridges Pi lifecycle events into Glint's agent status store.
// Installed by glint hooks install pi. Do not edit manually.

import { spawnSync } from "node:child_process";
import type { AgentEndEvent, ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const GLINT_BIN = %q;

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string" && value.trim().length > 0) return value.trim();
  }
  return undefined;
}

function textFromContent(content: unknown): string | undefined {
  if (typeof content === "string") return content.trim() || undefined;
  if (!Array.isArray(content)) return undefined;
  const parts: string[] = [];
  for (const block of content) {
    if (!block || typeof block !== "object") continue;
    const typed = block as { type?: unknown; text?: unknown };
    if (typed.type === "text" && typeof typed.text === "string") parts.push(typed.text);
  }
  return firstString(parts.join("\n"));
}

function lastAssistantMessage(event: AgentEndEvent): string | undefined {
  for (let index = event.messages.length - 1; index >= 0; index--) {
    const message = event.messages[index] as { role?: unknown; content?: unknown } | undefined;
    if (!message || message.role !== "assistant") continue;
    const text = textFromContent(message.content);
    if (text) return text;
  }
  return undefined;
}

function sessionId(ctx: ExtensionContext): string | undefined {
  const manager = ctx.sessionManager as unknown as {
    getSessionId?: () => string | undefined;
    getSessionFile?: () => string | undefined;
  };
  return firstString(manager.getSessionId?.(), manager.getSessionFile?.());
}

function sendHook(event: string, ctx: ExtensionContext, extra: Record<string, unknown> = {}): void {
  if (process.env.GLINT_PI_HOOKS_DISABLED === "1") return;
  const cwd = firstString(ctx.cwd, process.cwd()) ?? process.cwd();
  const payload = {
    session_id: sessionId(ctx),
    cwd,
    hook_event_name: event,
    ...extra,
  };
  try {
    spawnSync(GLINT_BIN, ["hook", "pi", event, "--workspace", cwd], {
      input: JSON.stringify(payload),
      encoding: "utf8",
      stdio: ["pipe", "ignore", "ignore"],
      timeout: 5000,
      env: { ...process.env, GLINT_WORKSPACE: cwd },
    });
  } catch (_) {}
}

export default function glintPiSessionExtension(pi: ExtensionAPI) {
  pi.on("before_agent_start", async (event, ctx) => {
    sendHook("prompt-submit", ctx, { prompt: event.prompt });
  });

  pi.on("agent_end", async (_event, ctx) => {
    sendHook("stop", ctx);
  });
}
`, glintPiExtensionMarker, glintBin)
}
