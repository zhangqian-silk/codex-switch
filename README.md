# codex-switch

Manage and switch multiple Codex accounts.

`codex-switch` stores named auth snapshots, switches the active `auth.json`, exports isolated `CODEX_HOME` directories, and can run `codex` against a selected account. It works with Windows, Linux, and macOS.

## Install

Build locally:

```bash
go build -o codex-switch .
```

Or install with Go:

```bash
go install .
```

## Quick Start

Save the current account:

```bash
codex-switch add work
```

List saved accounts:

```bash
codex-switch list
```

Switch accounts:

```bash
codex-switch switch work
```

Start the interactive shell:

```bash
codex-switch
```

## Commands

```text
codex-switch add [name] [--force]
codex-switch login [name] [--force]
codex-switch import [name] <auth.json> [--force]
codex-switch list
codex-switch current
codex-switch status
codex-switch switch [name] [--login] [--force]
codex-switch rename <old> <new>
codex-switch remove [name]
codex-switch export [name] <auth.json>
codex-switch export-home [name] <dir> [--no-copy-config]
codex-switch run <name> [--home <dir>] [--cd <dir>] [--no-copy-config] [-- <codex args...>]
codex-switch paths [subcommand]
```

## Path Profiles

`codex-switch` can manage multiple active auth locations. A path profile points to a `CODEX_HOME` directory or an `auth.json` file.

Show the active path and stored profiles:

```bash
codex-switch paths
```

Add and select a profile:

```bash
codex-switch paths add work ~/.codex-work --use
```

Switch between profiles:

```bash
codex-switch paths use work
codex-switch paths use personal
```

Remove or reset the selected profile:

```bash
codex-switch paths remove personal
codex-switch paths reset
```

`CODEX_HOME` still has highest priority. If it is set in the current shell, it overrides the selected path profile for that session.

## Interactive Shell

Run `codex-switch` with no arguments to enter the interactive shell.

- Commands must start with `/`, such as `/help`, `/switch`, `/exit`
- Supports command prefix matching such as `/sw`, `/st`, `/ren`
- Shows live command suggestions while you type in a real terminal
- Supports `Tab` to complete the selected command
- Supports `Up` / `Down` to move through suggestions
- Supports `Right Arrow` to accept the selected completion
- Supports `Esc` to hide the suggestion list
- Shows grouped suggestions for commands, accounts, and path profiles
- Shows candidate commands when a prefix is ambiguous
- Typing `/` prints the available command list
- Shows inline parameter hints for commands such as `/paths add`, `/run`, and `/export-home`
- Displays the current context in the prompt, for example `codex-switch[work]>` or `codex-switch[untracked]>`
- Supports `/exit`, `/quit`, and `/q`

## Examples

Import an auth file:

```bash
codex-switch import personal ./personal-auth.json
```

Export an isolated home:

```bash
codex-switch export-home work ./codex-work
```

Run Codex with an isolated home:

```bash
codex-switch run work -- -C ./my-project
```

Inspect the active auth path:

```bash
codex-switch current
codex-switch paths current
```

## Notes

- The active auth file path is shown in `current`, `paths`, and after `switch`
- Stored data lives under `CODEX_SWITCH_HOME` when set, otherwise under the platform user config directory
- `switch` keeps newer tokens when the live session is fresher than the saved snapshot
- `untracked` means the current active `auth.json` exists, but it is not yet saved as a managed account in `codex-switch`
