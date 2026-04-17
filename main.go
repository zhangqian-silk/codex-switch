package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex-switch/internal/app"
	"codex-switch/internal/codex"
	"codex-switch/internal/store"
	"golang.org/x/term"
)

var loginAccount = func(application *app.App, name string, force bool) (*store.Record, error) {
	return application.Login(name, force)
}

var switchAccount = func(application *app.App, name string) (*store.Record, string, error) {
	return application.Switch(name)
}

var exportAuthAccount = func(application *app.App, name, targetPath string) (*store.Record, string, error) {
	return application.ExportAuth(name, targetPath)
}

var exportHomeAccount = func(application *app.App, name, dir string, copyConfig bool) (*store.Record, string, error) {
	return application.ExportHome(name, dir, copyConfig)
}

var runAccount = func(application *app.App, name string, opts app.RunOptions) (string, error) {
	return application.Run(name, opts)
}

var newApplication = app.New

type shellCommandSpec struct {
	Name    string
	Aliases []string
	Summary string
}

type realtimeCommandSuggestions struct {
	Hint    string
	Matches []realtimeSuggestionItem
}

type realtimeSuggestionItem struct {
	Group   string
	Value   string
	Summary string
}

type realtimeShellSession struct {
	input        *os.File
	state        *term.State
	suspendDepth int
}

var activeRealtimeShellSession *realtimeShellSession

var termMakeRaw = term.MakeRaw
var termRestore = term.Restore

var errExitInteractiveShell = errors.New("exit interactive shell")

type terminalUI struct {
	writer io.Writer
	styled bool
}

func newTerminalUI(writer io.Writer) terminalUI {
	styled := false
	if file, ok := writer.(*os.File); ok {
		styled = term.IsTerminal(int(file.Fd()))
	}
	return terminalUI{writer: writer, styled: styled}
}

func uiWriter(application *app.App) io.Writer {
	if application != nil && application.Stdout != nil {
		return application.Stdout
	}
	return os.Stdout
}

func uiForApp(application *app.App) terminalUI {
	return newTerminalUI(uiWriter(application))
}

func (ui terminalUI) Print(text string) {
	_, _ = fmt.Fprint(ui.writer, text)
}

func (ui terminalUI) Println(text string) {
	_, _ = fmt.Fprintln(ui.writer, text)
}

func (ui terminalUI) Printf(format string, args ...any) {
	_, _ = fmt.Fprintf(ui.writer, format, args...)
}

func (ui terminalUI) style(text string, codes ...string) string {
	if !ui.styled || text == "" {
		return text
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + text + "\x1b[0m"
}

func (ui terminalUI) strong(text string) string  { return ui.style(text, "1", "97") }
func (ui terminalUI) muted(text string) string   { return ui.style(text, "90") }
func (ui terminalUI) accent(text string) string  { return ui.style(text, "96") }
func (ui terminalUI) success(text string) string { return ui.style(text, "92") }
func (ui terminalUI) warning(text string) string { return ui.style(text, "93") }
func (ui terminalUI) danger(text string) string  { return ui.style(text, "91") }

func (ui terminalUI) link(text, target string) string {
	if !ui.styled || strings.TrimSpace(text) == "" || strings.TrimSpace(target) == "" {
		return text
	}
	return "\x1b]8;;" + target + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func (ui terminalUI) sectionTitle(title string) string {
	if ui.styled {
		return ui.strong(title)
	}
	return "== " + title + " =="
}

func (ui terminalUI) badge(text, tone string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	plain := "[" + text + "]"
	switch tone {
	case "success":
		return ui.success(plain)
	case "warning":
		return ui.warning(plain)
	case "danger":
		return ui.danger(plain)
	case "muted":
		return ui.muted(plain)
	default:
		return ui.accent(plain)
	}
}

func formatDetailLine(ui terminalUI, label, value string) string {
	return "  " + ui.muted(fmt.Sprintf("%-10s", label)) + " " + value
}

func formatItemHeader(ui terminalUI, current bool, title string, badges ...string) string {
	marker := ui.muted("-")
	if current {
		marker = ui.success("*")
	}
	parts := make([]string, 0, len(badges))
	for _, badge := range badges {
		if strings.TrimSpace(badge) != "" {
			parts = append(parts, badge)
		}
	}
	line := marker + " " + ui.strong(title)
	if len(parts) > 0 {
		line += " " + strings.Join(parts, " ")
	}
	return line
}

func quotaTone(percent int) string {
	switch {
	case percent >= 70:
		return "success"
	case percent >= 30:
		return "warning"
	default:
		return "danger"
	}
}

func formatQuotaValue(ui terminalUI, percent int) string {
	return ui.badge(fmt.Sprintf("%d%%", percent), quotaTone(percent))
}

func planBadge(ui terminalUI, plan string) string {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "":
		return ""
	case "plus", "pro", "team":
		return ui.badge(plan, "success")
	case "free":
		return ui.badge(plan, "muted")
	default:
		return ui.badge(plan, "accent")
	}
}

func aliasBadge(ui terminalUI, name string) string {
	shellName := store.ShellName(name)
	if shellName == "" || strings.EqualFold(shellName, name) {
		return ""
	}
	return ui.badge(shellName, "accent")
}

var interactiveShellCommands = []shellCommandSpec{
	{Name: "help", Aliases: []string{"h", "?"}, Summary: "显示帮助"},
	{Name: "add", Summary: "保存当前登录态"},
	{Name: "login", Summary: "发起新的 Codex 登录并保存"},
	{Name: "import", Summary: "导入已有 auth.json"},
	{Name: "list", Aliases: []string{"ls"}, Summary: "列出已保存账号"},
	{Name: "current", Summary: "显示当前账号"},
	{Name: "status", Summary: "查询账号额度"},
	{Name: "switch", Aliases: []string{"use"}, Summary: "切换账号"},
	{Name: "rename", Aliases: []string{"mv"}, Summary: "重命名账号"},
	{Name: "remove", Aliases: []string{"rm", "delete"}, Summary: "删除账号"},
	{Name: "export", Summary: "导出 auth.json"},
	{Name: "export-home", Summary: "导出独立 home"},
	{Name: "run", Summary: "使用独立 home 运行 codex"},
	{Name: "paths", Summary: "显示或管理 auth path"},
	{Name: "exit", Aliases: []string{"quit", "q"}, Summary: "退出交互 Shell"},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	application, err := newApplication()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		err = runInteractiveShell(application)
	} else {
		err = runWithApp(application, args)
	}
	if errors.Is(err, errExitInteractiveShell) {
		return nil
	}
	return err
}

func runWithApp(application *app.App, args []string) error {
	return runCommand(application, args, false)
}

func runCommand(application *app.App, args []string, interactive bool) error {
	switch args[0] {
	case "help", "-h", "--help", "?":
		printUsage()
		return nil
	case "paths":
		return handlePaths(application, args[1:])
	case "add":
		return handleAdd(application, args[1:])
	case "login":
		return handleLogin(application, args[1:])
	case "import":
		return handleImport(application, args[1:])
	case "list", "ls":
		return handleList(application, args[1:])
	case "current":
		return handleCurrent(application)
	case "status":
		return handleStatus(application, args[1:])
	case "switch", "use":
		return handleSwitch(application, args[1:])
	case "rename", "mv":
		return handleRename(application, args[1:])
	case "remove", "rm", "delete":
		return handleRemove(application, args[1:])
	case "export":
		return handleExport(application, args[1:])
	case "export-home":
		return handleExportHome(application, args[1:])
	case "run":
		return handleRun(application, args[1:])
	default:
		if !interactive {
			printUsage()
			return fmt.Errorf("未知命令: %s", args[0])
		}
		return fmt.Errorf("未知命令: %s。输入 /help 查看可用命令", args[0])
	}
}

func printUsage() {
	fmt.Println(usageText())
}

func usageText() string {
	return `codex-switch

Manage and switch multiple Codex accounts.

Usage:
  codex-switch
  codex-switch <command> [arguments]

Commands:
  add [名称] [--force]                             保存当前登录态
  login [名称] [--force]                           发起新的 Codex 登录并保存
  import [名称] <auth.json> [--force]              导入已有 auth.json
  list                                             列出已保存账号
  current                                          显示当前账号
  status                                           查询账号额度
  switch [名称] [--login] [--force]                切换账号；省略名称时交互选择
  rename <旧名称> <新名称>                         重命名账号
  rename --from <旧名称> --to <新名称>             重命名账号
  remove [名称]                                    删除账号；省略名称时交互选择
  export [名称] <auth.json>                        导出 auth.json；省略名称时交互选择
  export-home [名称] <目录> [--no-copy-config]     导出独立 home；省略名称时交互选择
  run <名称> [--home <目录>] [--cd <目录>] [--no-copy-config] [-- <codex 参数...>]
                                                   使用独立 home 运行 codex；省略名称时交互选择
  paths [subcommand]                               显示或管理 auth path
  help                                             显示帮助

Interactive Shell:
  直接运行 codex-switch 可进入交互 Shell。
  所有命令都必须以 / 开头，例如 /help、/switch、/exit。
  输入时会实时展示匹配建议，可用 Tab、右方向键或上下键加回车接收补全。
  输入 / 可显示全部命令，输入 /exit、/quit 或 /q 退出。

Examples:
  codex-switch add work
  codex-switch add
  codex-switch login new-work
  codex-switch login
  codex-switch import personal ./personal-auth.json
  codex-switch import ./personal-auth.json
  codex-switch list
  codex-switch status
  codex-switch switch
  codex-switch switch work
  codex-switch switch --login new-work
  codex-switch rename --from "Jerry Butler" --to jerry-butler
  codex-switch remove
  codex-switch paths add work ~/.codex-work --use
  codex-switch export-home work ./codex-work
  codex-switch run work -- -C ./my-project`
}

func runInteractiveShell(application *app.App) error {
	writer := uiWriter(application)
	ui := newTerminalUI(writer)
	if _, err := fmt.Fprintln(writer, ui.sectionTitle("codex-switch shell")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, ui.muted("Commands start with /. Type / to browse commands, /help for help, and /exit to quit.")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Context: %s\n", ui.accent(interactiveShellContext(application))); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}

	if canUseRealtimeShell(application) {
		return runRealtimeInteractiveShell(application)
	}

	return runLineInteractiveShell(application, writer)
}

func runLineInteractiveShell(application *app.App, writer io.Writer) error {
	reader := inputReader(application)
	for {
		if _, err := fmt.Fprint(writer, interactiveShellPrompt(application)); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				return nil
			}
			continue
		}

		shouldExit, handleErr := handleInteractiveLine(application, writer, line)
		if handleErr != nil {
			return handleErr
		}
		if shouldExit || err == io.EOF {
			return nil
		}
	}
}

func runRealtimeInteractiveShell(application *app.App) error {
	inputFile := interactiveInputFile(application)
	outputFile := interactiveOutputFile(application)
	if inputFile == nil || outputFile == nil {
		return runLineInteractiveShell(application, outputFile)
	}

	state, err := termMakeRaw(int(inputFile.Fd()))
	if err != nil {
		return runLineInteractiveShell(application, outputFile)
	}
	activeRealtimeShellSession = &realtimeShellSession{
		input: inputFile,
		state: state,
	}
	defer func() {
		activeRealtimeShellSession = nil
		_ = termRestore(int(inputFile.Fd()), state)
		fmt.Fprint(outputFile, "\r\n")
	}()

	buffer := make([]byte, 0, 128)
	renderedLines := 0
	selectedSuggestion := -1
	selectionArmed := false
	suggestionsVisible := true
	for {
		renderedLines, selectedSuggestion, err = renderInteractiveInput(outputFile, renderedLines, application, interactiveShellPrompt(application), string(buffer), selectedSuggestion, selectionArmed, suggestionsVisible)
		if err != nil {
			return err
		}

		var b [1]byte
		if _, err := inputFile.Read(b[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch b[0] {
		case '\r', '\n':
			executionLine := interactiveExecutionLine(application, buffer, selectedSuggestion, selectionArmed, suggestionsVisible)
			if renderedLines, _, err = renderInteractiveInput(outputFile, renderedLines, application, interactiveShellPrompt(application), executionLine, -1, false, false); err != nil {
				return err
			}
			fmt.Fprint(outputFile, "\r\n")
			renderedLines = 0
			line := strings.TrimSpace(executionLine)
			buffer = buffer[:0]
			selectedSuggestion = -1
			selectionArmed = false
			suggestionsVisible = true
			if line == "" {
				continue
			}
			shouldExit, err := handleInteractiveLine(application, outputFile, line)
			if err != nil {
				return err
			}
			if shouldExit {
				return nil
			}
		case 3:
			return nil
		case 8, 127:
			if len(buffer) > 0 {
				buffer = buffer[:len(buffer)-1]
			}
			selectedSuggestion = -1
			selectionArmed = false
			suggestionsVisible = true
		case '\t':
			completed := autocompleteInteractiveLine(application, string(buffer), selectedSuggestion)
			if completed != string(buffer) {
				buffer = []byte(completed)
			}
			selectedSuggestion = -1
			selectionArmed = false
			suggestionsVisible = true
		case 27:
			action := readEscapeAction(inputFile)
			switch action {
			case "up":
				suggestionsVisible = true
				selectedSuggestion = moveSuggestionSelectionWithApp(application, string(buffer), selectedSuggestion, -1)
				selectionArmed = true
			case "down":
				suggestionsVisible = true
				selectedSuggestion = moveSuggestionSelectionWithApp(application, string(buffer), selectedSuggestion, 1)
				selectionArmed = true
			case "right":
				completed := autocompleteInteractiveLine(application, string(buffer), selectedSuggestion)
				if completed != string(buffer) {
					buffer = []byte(completed)
				}
				suggestionsVisible = true
				selectedSuggestion = -1
				selectionArmed = false
			case "escape":
				suggestionsVisible = false
				selectedSuggestion = -1
				selectionArmed = false
			}
		default:
			if b[0] >= 32 && b[0] != 255 {
				buffer = append(buffer, b[0])
				suggestionsVisible = true
				selectedSuggestion = -1
				selectionArmed = false
			}
		}
	}
}

func handleInteractiveLine(application *app.App, writer io.Writer, line string) (bool, error) {
	args, parseErr := parseInteractiveCommand(line)
	if parseErr != nil {
		_, _ = fmt.Fprintf(writer, "错误: %v\n", parseErr)
		return false, nil
	}
	if len(args) == 0 {
		return false, nil
	}

	command, resolveErr := resolveInteractiveCommand(application, args[0])
	if resolveErr != nil {
		_, _ = fmt.Fprintf(writer, "错误: %v\n", resolveErr)
		return false, nil
	}
	args[0] = command

	if command == "exit" || command == "quit" || command == "q" {
		return true, nil
	}
	if command == "help" {
		_, _ = fmt.Fprintln(writer, interactiveShellCommandTable())
		return false, nil
	}

	if runErr := runInteractiveShellCommand(application, args); runErr != nil {
		if errors.Is(runErr, errExitInteractiveShell) {
			return true, nil
		}
		_, _ = fmt.Fprintf(writer, "错误: %v\n", runErr)
	}
	return false, nil
}

func runInteractiveShellCommand(application *app.App, args []string) (err error) {
	resume, err := suspendRealtimeShellRawMode(application)
	if err != nil {
		return err
	}
	defer func() {
		if resumeErr := resume(); err == nil && resumeErr != nil {
			err = resumeErr
		}
	}()

	return runCommand(application, args, true)
}

func canUseRealtimeShell(application *app.App) bool {
	inputFile := interactiveInputFile(application)
	outputFile := interactiveOutputFile(application)
	if inputFile == nil || outputFile == nil {
		return false
	}
	return term.IsTerminal(int(inputFile.Fd())) && term.IsTerminal(int(outputFile.Fd()))
}

func interactiveInputFile(application *app.App) *os.File {
	if application != nil {
		if file, ok := application.Stdin.(*os.File); ok {
			return file
		}
	}
	return nil
}

func interactiveOutputFile(application *app.App) *os.File {
	if application != nil {
		if file, ok := application.Stdout.(*os.File); ok {
			return file
		}
	}
	return os.Stdout
}

func renderInteractiveInput(writer io.Writer, previousLines int, application *app.App, prompt, line string, selected int, armed, visible bool) (int, int, error) {
	ui := newTerminalUI(writer)
	suggestions := interactiveRealtimeSuggestions(application, line)
	if !visible {
		suggestions = realtimeCommandSuggestions{}
	}
	if armed {
		selected = normalizeSuggestionSelection(len(suggestions.Matches), selected)
	} else {
		selected = -1
	}
	lines := renderInteractiveSuggestionLines(ui, suggestions, selected)
	if previousLines > 0 {
		if _, err := fmt.Fprintf(writer, "\x1b[%dA", previousLines); err != nil {
			return 0, selected, err
		}
	}
	if _, err := fmt.Fprint(writer, "\x1b[?25l"); err != nil {
		return 0, selected, err
	}
	defer fmt.Fprint(writer, "\x1b[?25h")

	for _, suggestion := range lines {
		if _, err := fmt.Fprintf(writer, "\r\x1b[2K%s\n", suggestion); err != nil {
			return 0, selected, err
		}
	}
	if _, err := fmt.Fprintf(writer, "\r\x1b[2K%s%s", prompt, line); err != nil {
		return 0, selected, err
	}
	extraLines := previousLines - len(lines)
	if extraLines > 0 {
		if _, err := fmt.Fprint(writer, "\x1b[s"); err != nil {
			return 0, selected, err
		}
		for i := 0; i < extraLines; i++ {
			if _, err := fmt.Fprint(writer, "\n\r\x1b[2K"); err != nil {
				return 0, selected, err
			}
		}
		if _, err := fmt.Fprint(writer, "\x1b[u"); err != nil {
			return 0, selected, err
		}
	}
	return len(lines), selected, nil
}

func parseInteractiveCommand(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	if line == "/" {
		return []string{"help"}, nil
	}
	if !strings.HasPrefix(line, "/") {
		return nil, fmt.Errorf("交互命令需以 / 开头；输入 /help 查看命令")
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "/"))
	if line == "" {
		return []string{"help"}, nil
	}
	return splitCommandLine(line)
}

func interactiveShellPrompt(application *app.App) string {
	context := interactiveShellContext(application)
	ui := uiForApp(application)
	if !ui.styled {
		return fmt.Sprintf("codex-switch[%s]> ", context)
	}
	return ui.muted("codex-switch") + ui.accent("["+context+"]") + ui.success("> ")
}

func interactiveShellContext(application *app.App) string {
	if application == nil {
		return "unknown"
	}

	status, err := application.Current()
	if err != nil {
		return "unknown"
	}
	if status.Managed != nil {
		return displayPromptName(status.Managed.Name)
	}
	if status.Live != nil {
		return "untracked"
	}
	return "no-account"
}

func displayPromptName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}
	if shellName := store.ShellName(name); shellName != "" {
		return shellName
	}
	return name
}

func interactiveShellCommandTable() string {
	ui := newTerminalUI(os.Stdout)
	var builder strings.Builder
	builder.WriteString("Available commands:\n")
	for _, command := range interactiveShellCommands {
		aliasText := ""
		if len(command.Aliases) > 0 {
			prefixedAliases := make([]string, 0, len(command.Aliases))
			for _, alias := range command.Aliases {
				prefixedAliases = append(prefixedAliases, "/"+alias)
			}
			aliasText = ui.muted(" [" + strings.Join(prefixedAliases, ", ") + "]")
		}
		builder.WriteString(fmt.Sprintf("  %-12s %-20s %s\n", ui.accent("/"+command.Name), aliasText, command.Summary))
	}
	builder.WriteString(ui.muted("Tip: 输入 / 查看全部命令，输入时会实时显示建议并可直接接收补全。"))
	return builder.String()
}

func interactiveRealtimeSuggestions(application *app.App, line string) realtimeCommandSuggestions {
	hasTrailingSpace := strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t")
	line = strings.TrimLeft(line, " \t")
	if strings.TrimSpace(line) == "" {
		return realtimeCommandSuggestions{}
	}
	if !strings.HasPrefix(line, "/") {
		return realtimeCommandSuggestions{Hint: "hint: commands in shell must start with /"}
	}

	token := strings.TrimPrefix(line, "/")
	fields := strings.Fields(token)

	if len(fields) == 0 {
		return realtimeCommandSuggestions{Matches: commandSuggestionItems(interactiveShellCommands)}
	}

	commandToken := fields[0]
	if len(fields) == 1 && !hasTrailingSpace {
		if exact, ok := findInteractiveCommandExact(strings.ToLower(commandToken)); ok {
			return realtimeCommandSuggestions{Matches: commandSuggestionItems([]shellCommandSpec{exact})}
		}
		matches := findInteractiveCommandMatches(strings.ToLower(commandToken))
		if len(matches) == 0 {
			return realtimeCommandSuggestions{Hint: "hint: no matching commands"}
		}
		return realtimeCommandSuggestions{Matches: commandSuggestionItems(matches)}
	}

	command, resolved := resolveInteractiveCommandToken(commandToken)
	if !resolved {
		matches := findInteractiveCommandMatches(strings.ToLower(commandToken))
		if len(matches) == 0 {
			return realtimeCommandSuggestions{Hint: "hint: no matching commands"}
		}
		return realtimeCommandSuggestions{Matches: commandSuggestionItems(matches)}
	}
	paramSuggestions := interactiveParameterSuggestions(application, command.Name, fields, hasTrailingSpace)
	if len(paramSuggestions.Matches) > 0 || paramSuggestions.Hint != "" {
		return paramSuggestions
	}
	return realtimeCommandSuggestions{Matches: commandSuggestionItems([]shellCommandSpec{command})}
}

func renderInteractiveSuggestionLines(ui terminalUI, suggestions realtimeCommandSuggestions, selected int) []string {
	if strings.TrimSpace(suggestions.Hint) != "" {
		return []string{ui.muted(suggestions.Hint)}
	}
	if len(suggestions.Matches) == 0 {
		return nil
	}

	lines := make([]string, 0, len(suggestions.Matches)+1)
	lines = append(lines, ui.muted("suggestions"))
	lastGroup := ""
	for index, suggestion := range suggestions.Matches {
		if suggestion.Group != "" && suggestion.Group != lastGroup {
			lines = append(lines, "  "+ui.accent("["+suggestion.Group+"]"))
			lastGroup = suggestion.Group
		}
		prefix := "  "
		if index == selected {
			prefix = ui.accent("> ")
		} else {
			prefix = ui.muted("  ")
		}
		value := fmt.Sprintf("%-18s", suggestion.Value)
		line := fmt.Sprintf("%s%s %s", prefix, ui.strong(value), ui.muted(suggestion.Summary))
		if index == selected {
			line = "\x1b[7m" + line + "\x1b[0m"
		}
		lines = append(lines, line)
	}
	return lines
}

func normalizeSuggestionSelection(count, selected int) int {
	if count <= 0 {
		return -1
	}
	if selected < 0 || selected >= count {
		return 0
	}
	return selected
}

func moveSuggestionSelection(line string, selected, delta int) int {
	return moveSuggestionSelectionWithApp(nil, line, selected, delta)
}

func moveSuggestionSelectionWithApp(application *app.App, line string, selected, delta int) int {
	suggestions := interactiveRealtimeSuggestions(application, line)
	count := len(suggestions.Matches)
	if count == 0 {
		return -1
	}
	if selected < 0 || selected >= count {
		if delta < 0 {
			return count - 1
		}
		return 0
	}
	selected = normalizeSuggestionSelection(count, selected)
	selected += delta
	if selected < 0 {
		selected = count - 1
	}
	if selected >= count {
		selected = 0
	}
	return selected
}

func autocompleteInteractiveLine(application *app.App, line string, selected int) string {
	suggestions := interactiveRealtimeSuggestions(application, line)
	if len(suggestions.Matches) == 0 {
		return line
	}

	if selected < 0 || selected >= len(suggestions.Matches) {
		selected = 0
	}
	value := suggestions.Matches[selected].Value
	trimmedRight := strings.TrimRight(line, " \t")
	if !strings.HasPrefix(strings.TrimSpace(trimmedRight), "/") {
		return line
	}

	start, end := interactiveCompletionRange(line)
	completed := line[:start] + value + line[end:]
	if !strings.HasSuffix(completed, " ") {
		completed += " "
	}
	return completed
}

func applySelectedSuggestionOnEnter(application *app.App, buffer []byte, selected int, armed, visible bool) []byte {
	if !armed || !visible {
		return buffer
	}
	completed := autocompleteInteractiveLine(application, string(buffer), selected)
	if completed == string(buffer) {
		return buffer
	}
	return []byte(completed)
}

func interactiveExecutionLine(application *app.App, buffer []byte, selected int, armed, visible bool) string {
	line := string(applySelectedSuggestionOnEnter(application, buffer, selected, armed, visible))
	return strings.TrimRight(line, " \t")
}

func interactiveCompletionRange(line string) (int, int) {
	end := len(line)
	if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
		return end, end
	}
	start := strings.LastIndexAny(line, " \t")
	if start < 0 {
		return 0, end
	}
	return start + 1, end
}

func resolveInteractiveCommandToken(query string) (shellCommandSpec, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return shellCommandSpec{}, false
	}
	if exact, ok := findInteractiveCommandExact(query); ok {
		return exact, true
	}
	matches := findInteractiveCommandMatches(query)
	if len(matches) == 1 {
		return matches[0], true
	}
	return shellCommandSpec{}, false
}

func commandSuggestionItems(commands []shellCommandSpec) []realtimeSuggestionItem {
	items := make([]realtimeSuggestionItem, 0, len(commands))
	for _, command := range commands {
		items = append(items, realtimeSuggestionItem{
			Group:   "commands",
			Value:   "/" + command.Name,
			Summary: command.Summary,
		})
	}
	return items
}

func interactiveParameterSuggestions(application *app.App, commandName string, fields []string, hasTrailingSpace bool) realtimeCommandSuggestions {
	switch commandName {
	case "paths":
		return interactivePathSuggestions(application, fields, hasTrailingSpace)
	case "switch", "remove", "export", "export-home", "run":
		return interactiveAccountSuggestions(application, commandName, fields, hasTrailingSpace)
	case "rename":
		return interactiveAccountSuggestions(application, commandName, fields, hasTrailingSpace)
	default:
		return realtimeCommandSuggestions{}
	}
}

func interactivePathSuggestions(application *app.App, fields []string, hasTrailingSpace bool) realtimeCommandSuggestions {
	subcommands := []realtimeSuggestionItem{
		{Group: "paths", Value: "add", Summary: "新增 path profile"},
		{Group: "paths", Value: "current", Summary: "显示当前 active path"},
		{Group: "paths", Value: "list", Summary: "列出 path profiles"},
		{Group: "paths", Value: "remove", Summary: "删除 path profile"},
		{Group: "paths", Value: "reset", Summary: "清除当前 path profile 选择"},
		{Group: "paths", Value: "use", Summary: "切换当前 path profile"},
	}

	if len(fields) == 1 && hasTrailingSpace {
		return realtimeCommandSuggestions{Matches: subcommands}
	}
	if len(fields) == 2 && !hasTrailingSpace {
		return realtimeCommandSuggestions{Matches: filterSuggestionItems(subcommands, fields[1])}
	}

	subcommand := ""
	if len(fields) >= 2 {
		subcommand = strings.ToLower(strings.TrimSpace(fields[1]))
	}
	switch subcommand {
	case "use", "remove":
		profiles := listPathProfileSuggestions(application)
		if len(fields) == 2 && hasTrailingSpace {
			return realtimeCommandSuggestions{Matches: profiles}
		}
		if len(fields) >= 3 && !hasTrailingSpace {
			return realtimeCommandSuggestions{Matches: filterSuggestionItems(profiles, fields[len(fields)-1])}
		}
	case "add":
		if len(fields) == 2 && hasTrailingSpace {
			return realtimeCommandSuggestions{Hint: "hint: /paths add <name> <CODEX_HOME|auth.json> [--use]"}
		}
		if len(fields) == 3 && hasTrailingSpace {
			return realtimeCommandSuggestions{Hint: "hint: type a CODEX_HOME directory or auth.json path"}
		}
	}
	return realtimeCommandSuggestions{}
}

func interactiveAccountSuggestions(application *app.App, commandName string, fields []string, hasTrailingSpace bool) realtimeCommandSuggestions {
	accountItems := listAccountSuggestions(application)
	if len(accountItems) == 0 {
		return realtimeCommandSuggestions{}
	}

	positionals := extractInteractivePositionals(commandName, fields[1:])
	if len(positionals) == 0 && hasTrailingSpace {
		return realtimeCommandSuggestions{Matches: accountItems}
	}
	if len(positionals) == 1 && !hasTrailingSpace {
		return realtimeCommandSuggestions{Matches: filterSuggestionItems(accountItems, positionals[0])}
	}
	switch commandName {
	case "run":
		if len(positionals) >= 1 && hasTrailingSpace {
			return realtimeCommandSuggestions{Hint: "hint: /run <account> [--home <dir>] [--cd <dir>] [-- <codex args...>]"}
		}
	case "export-home":
		if len(positionals) == 1 && hasTrailingSpace {
			return realtimeCommandSuggestions{Matches: exportHomePathSuggestionItems(application, positionals[0])}
		}
		if len(positionals) >= 2 && !hasTrailingSpace {
			return realtimeCommandSuggestions{Matches: filterSuggestionItems(exportHomePathSuggestionItems(application, positionals[0]), positionals[len(positionals)-1])}
		}
		if len(positionals) >= 1 && hasTrailingSpace {
			return realtimeCommandSuggestions{Hint: "hint: /export-home <account> <dir> [--no-copy-config]"}
		}
	case "export":
		if len(positionals) == 1 && hasTrailingSpace {
			return realtimeCommandSuggestions{Matches: exportAuthPathSuggestionItems(application, positionals[0])}
		}
		if len(positionals) >= 2 && !hasTrailingSpace {
			return realtimeCommandSuggestions{Matches: filterSuggestionItems(exportAuthPathSuggestionItems(application, positionals[0]), positionals[len(positionals)-1])}
		}
		if len(positionals) >= 1 && hasTrailingSpace {
			return realtimeCommandSuggestions{Hint: "hint: /export <account> <auth.json path>"}
		}
	case "rename":
		if len(positionals) >= 1 && hasTrailingSpace {
			return realtimeCommandSuggestions{Hint: "hint: /rename <old-name> <new-name>"}
		}
	}
	return realtimeCommandSuggestions{}
}

func extractInteractivePositionals(commandName string, args []string) []string {
	positionals := make([]string, 0, len(args))
	switch commandName {
	case "switch":
		for _, arg := range args {
			if arg == "--login" || arg == "--force" {
				continue
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			positionals = append(positionals, arg)
		}
	case "run":
		skipNext := false
		for _, arg := range args {
			if skipNext {
				skipNext = false
				continue
			}
			switch arg {
			case "--home", "--cd":
				skipNext = true
				continue
			case "--copy-config", "--no-copy-config":
				continue
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			positionals = append(positionals, arg)
			break
		}
	default:
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			positionals = append(positionals, arg)
			break
		}
	}
	return positionals
}

func listAccountSuggestions(application *app.App) []realtimeSuggestionItem {
	if application == nil {
		return nil
	}
	records, err := application.List()
	if err != nil {
		return nil
	}
	items := make([]realtimeSuggestionItem, 0, len(records))
	for _, record := range records {
		value := record.Name
		if shellName := store.ShellName(record.Name); shellName != "" {
			value = shellName
		}
		summary := displayOr(record.Snapshot.AccountName, record.Name)
		if record.Snapshot.Email != "" {
			summary += " <" + record.Snapshot.Email + ">"
		}
		items = append(items, realtimeSuggestionItem{Group: "accounts", Value: value, Summary: summary})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Value < items[j].Value
	})
	return items
}

func listPathProfileSuggestions(application *app.App) []realtimeSuggestionItem {
	if application == nil || application.Store == nil {
		return nil
	}
	profiles, _, err := application.Store.ListPathProfiles()
	if err != nil {
		return nil
	}
	items := make([]realtimeSuggestionItem, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, realtimeSuggestionItem{
			Group:   "profiles",
			Value:   profile.Name,
			Summary: profile.Home,
		})
	}
	return items
}

func filterSuggestionItems(items []realtimeSuggestionItem, query string) []realtimeSuggestionItem {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	filtered := make([]realtimeSuggestionItem, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item.Value), query) {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func readEscapeAction(inputFile *os.File) string {
	seq, ok := readEscapeSequence(inputFile)
	if !ok {
		return "escape"
	}
	switch seq {
	case "[A":
		return "up"
	case "[B":
		return "down"
	case "[C":
		return "right"
	default:
		return "escape"
	}
}

func readEscapeSequence(inputFile *os.File) (string, bool) {
	_ = inputFile.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	defer inputFile.SetReadDeadline(time.Time{})

	var first [1]byte
	if _, err := inputFile.Read(first[:]); err != nil {
		return "", false
	}
	if first[0] != '[' {
		return string(first[:]), true
	}

	var second [1]byte
	if _, err := inputFile.Read(second[:]); err != nil {
		return string(first[:]), true
	}
	return "[" + string(second[:]), true
}

func suspendRealtimeShellRawMode(application *app.App) (func() error, error) {
	session := activeRealtimeShellSession
	if session == nil || session.input == nil || session.state == nil {
		return func() error { return nil }, nil
	}

	inputFile := interactiveInputFile(application)
	if inputFile != nil && inputFile != session.input {
		return func() error { return nil }, nil
	}

	if session.suspendDepth > 0 {
		session.suspendDepth++
		return func() error {
			session.suspendDepth--
			return nil
		}, nil
	}

	if err := termRestore(int(session.input.Fd()), session.state); err != nil {
		return nil, err
	}
	session.suspendDepth = 1

	return func() error {
		if session.suspendDepth > 1 {
			session.suspendDepth--
			return nil
		}

		state, err := termMakeRaw(int(session.input.Fd()))
		if err != nil {
			return err
		}
		session.suspendDepth = 0
		session.state = state
		return nil
	}, nil
}

func readPromptLine(application *app.App) (line string, err error) {
	resume, err := suspendRealtimeShellRawMode(application)
	if err != nil {
		return "", err
	}
	defer func() {
		if resumeErr := resume(); err == nil && resumeErr != nil {
			err = resumeErr
		}
	}()

	reader := inputReader(application)
	line, err = reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return line, nil
}

func promptControlAction(line string) string {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "/exit", "/quit", "/q", "退出", "/退出":
		return "exit"
	case "/cancel", "取消", "/取消":
		return "cancel"
	default:
		return ""
	}
}

func resolveInteractiveCommand(application *app.App, input string) (string, error) {
	query := strings.ToLower(strings.TrimSpace(input))
	if query == "" {
		return "help", nil
	}

	if exact, ok := findInteractiveCommandExact(query); ok {
		return exact.Name, nil
	}
	return "", fmt.Errorf("未知命令 %q。输入 /help 查看可用命令", "/"+input)
}

func findInteractiveCommandExact(query string) (shellCommandSpec, bool) {
	for _, command := range interactiveShellCommands {
		if strings.EqualFold(command.Name, query) {
			return command, true
		}
		for _, alias := range command.Aliases {
			if strings.EqualFold(alias, query) {
				return command, true
			}
		}
	}
	return shellCommandSpec{}, false
}

func findInteractiveCommandMatches(query string) []shellCommandSpec {
	matches := make([]shellCommandSpec, 0, len(interactiveShellCommands))
	for _, command := range interactiveShellCommands {
		if strings.HasPrefix(strings.ToLower(command.Name), query) {
			matches = append(matches, command)
			continue
		}
		for _, alias := range command.Aliases {
			if strings.HasPrefix(strings.ToLower(alias), query) {
				matches = append(matches, command)
				break
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})
	return matches
}

func promptCommandSelection(application *app.App, prefix string, matches []shellCommandSpec) (string, error) {
	if application == nil || application.Stdin == nil {
		return "", fmt.Errorf("命令前缀 %q 匹配到多个结果，请输入完整命令", "/"+prefix)
	}

	writer := io.Writer(os.Stdout)
	if application.Stdout != nil {
		writer = application.Stdout
	}

	if _, err := fmt.Fprintf(writer, "匹配到多个命令（%q）:\n", "/"+prefix); err != nil {
		return "", err
	}
	for index, command := range matches {
		aliasText := ""
		if len(command.Aliases) > 0 {
			prefixedAliases := make([]string, 0, len(command.Aliases))
			for _, alias := range command.Aliases {
				prefixedAliases = append(prefixedAliases, "/"+alias)
			}
			aliasText = " (" + strings.Join(prefixedAliases, ", ") + ")"
		}
		if _, err := fmt.Fprintf(writer, "  %d. %-12s %s%s\n", index+1, "/"+command.Name, command.Summary, aliasText); err != nil {
			return "", err
		}
	}

	for {
		if _, err := fmt.Fprint(writer, "输入序号或命令名，回车或 /cancel 取消，/exit 退出: "); err != nil {
			return "", err
		}

		line, readErr := readPromptLine(application)
		if readErr != nil {
			return "", readErr
		}
		switch promptControlAction(line) {
		case "exit":
			return "", errExitInteractiveShell
		case "cancel":
			return "", fmt.Errorf("已取消命令选择")
		}
		input := strings.ToLower(strings.TrimSpace(line))
		if input == "" {
			return "", fmt.Errorf("已取消命令选择")
		}
		if index, err := parseSelectionIndex(input, len(matches)); err == nil {
			return matches[index].Name, nil
		}
		if selected, ok := selectMatchingCommand(matches, input); ok {
			return selected.Name, nil
		}
		if _, err := fmt.Fprintf(writer, "未找到匹配命令 %q，请重新输入。\n", input); err != nil {
			return "", err
		}
		if readErr == io.EOF {
			return "", fmt.Errorf("已取消命令选择")
		}
	}
}

func selectMatchingCommand(matches []shellCommandSpec, query string) (shellCommandSpec, bool) {
	query = strings.TrimSpace(strings.TrimPrefix(query, "/"))
	if exact, ok := findCommandExactInSet(matches, query); ok {
		return exact, true
	}

	var prefixMatches []shellCommandSpec
	for _, command := range matches {
		if strings.HasPrefix(strings.ToLower(command.Name), query) {
			prefixMatches = append(prefixMatches, command)
			continue
		}
		for _, alias := range command.Aliases {
			if strings.HasPrefix(strings.ToLower(alias), query) {
				prefixMatches = append(prefixMatches, command)
				break
			}
		}
	}
	if len(prefixMatches) == 1 {
		return prefixMatches[0], true
	}
	return shellCommandSpec{}, false
}

func findCommandExactInSet(commands []shellCommandSpec, query string) (shellCommandSpec, bool) {
	for _, command := range commands {
		if strings.EqualFold(command.Name, query) {
			return command, true
		}
		for _, alias := range command.Aliases {
			if strings.EqualFold(alias, query) {
				return command, true
			}
		}
	}
	return shellCommandSpec{}, false
}

func splitCommandLine(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false

	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}

	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			current.WriteRune(r)
		}
	}

	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("引号未闭合")
	}
	flush()
	return args, nil
}

func handlePaths(application *app.App, args []string) error {
	fs := flag.NewFlagSet("paths", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	use := fs.Bool("use", false, "添加后立即设为当前 path profile")
	if err := fs.Parse(args); err != nil {
		return err
	}

	positionals := fs.Args()
	subcommand := "list"
	if len(positionals) > 0 {
		subcommand = strings.ToLower(positionals[0])
		positionals = positionals[1:]
	}

	switch subcommand {
	case "list":
		if len(positionals) != 0 {
			return fmt.Errorf("用法: codex-switch paths\n      codex-switch paths list")
		}
		return printPathsOverview(application)
	case "current":
		if len(positionals) != 0 {
			return fmt.Errorf("用法: codex-switch paths current")
		}
		return printCurrentPath(application)
	case "add":
		if len(positionals) != 2 {
			return fmt.Errorf("用法: codex-switch paths add <名称> <目录或auth.json> [--use]")
		}
		profile, err := application.Store.SavePathProfile(positionals[0], positionals[1], *use)
		if err != nil {
			return err
		}
		if err := application.RefreshActivePath(); err != nil {
			return err
		}
		ui := uiForApp(application)
		ui.Println(ui.sectionTitle(fmt.Sprintf("已保存 path profile %q", profile.Name)))
		ui.Println(formatDetailLine(ui, "Home", profile.Home))
		ui.Println(formatDetailLine(ui, "Auth", codex.AuthFilePath(profile.Home)))
		if *use {
			ui.Println(formatDetailLine(ui, "状态", "已设为当前 path profile"))
			if application.ActivePathSource == "env" {
				ui.Println(formatDetailLine(ui, "提示", "当前会话仍优先使用 CODEX_HOME="+application.ActiveHome))
			}
		}
		return nil
	case "use":
		if len(positionals) != 1 {
			return fmt.Errorf("用法: codex-switch paths use <名称>")
		}
		profile, err := application.Store.UsePathProfile(positionals[0])
		if err != nil {
			return err
		}
		if err := application.RefreshActivePath(); err != nil {
			return err
		}
		ui := uiForApp(application)
		ui.Println(ui.sectionTitle(fmt.Sprintf("已切换到 path profile %q", profile.Name)))
		ui.Println(formatDetailLine(ui, "Home", profile.Home))
		ui.Println(formatDetailLine(ui, "Auth", codex.AuthFilePath(profile.Home)))
		if application.ActivePathSource == "env" {
			ui.Println(formatDetailLine(ui, "提示", "当前会话仍优先使用 CODEX_HOME="+application.ActiveHome))
		}
		return nil
	case "remove", "rm", "delete":
		if len(positionals) != 1 {
			return fmt.Errorf("用法: codex-switch paths remove <名称>")
		}
		if err := application.Store.DeletePathProfile(positionals[0]); err != nil {
			return err
		}
		if err := application.RefreshActivePath(); err != nil {
			return err
		}
		uiForApp(application).Println(uiForApp(application).sectionTitle(fmt.Sprintf("已删除 path profile %q", positionals[0])))
		return nil
	case "reset":
		if len(positionals) != 0 {
			return fmt.Errorf("用法: codex-switch paths reset")
		}
		if err := application.Store.ClearActivePathProfile(); err != nil {
			return err
		}
		if err := application.RefreshActivePath(); err != nil {
			return err
		}
		uiForApp(application).Println(uiForApp(application).sectionTitle("已清除当前 path profile 选择"))
		return printCurrentPath(application)
	default:
		return fmt.Errorf("未知 paths 子命令: %s", subcommand)
	}
}

func printPathsOverview(application *app.App) error {
	ui := uiForApp(application)
	if err := printCurrentPath(application); err != nil {
		return err
	}
	ui.Println("")
	ui.Println(ui.sectionTitle("Store"))
	ui.Println(formatDetailLine(ui, "root", application.Store.Root()))
	ui.Println(formatDetailLine(ui, "accounts", application.Store.AccountsDir()))
	ui.Println(formatDetailLine(ui, "homes", application.Store.HomesDir()))
	ui.Println(formatDetailLine(ui, "backups", application.Store.BackupsDir()))
	ui.Println(formatDetailLine(ui, "profiles", application.Store.PathsConfigPath()))

	profiles, currentProfile, err := application.Store.ListPathProfiles()
	if err != nil {
		return err
	}
	ui.Println("")
	ui.Println(ui.sectionTitle("Path profiles"))
	if len(profiles) == 0 {
		ui.Println(formatDetailLine(ui, "状态", "(none)"))
		return nil
	}

	for index, profile := range profiles {
		if index > 0 {
			ui.Println("")
		}
		marker := " "
		badges := []string{}
		switch {
		case application.ActivePathSource == "profile" && strings.EqualFold(application.ActivePathProfile, profile.Name):
			marker = "*"
			badges = append(badges, ui.badge("active", "success"))
		case currentProfile != "" && strings.EqualFold(currentProfile, profile.Name):
			marker = ">"
			badges = append(badges, ui.badge("selected", "accent"))
		}
		ui.Println(formatItemHeader(ui, marker == "*", profile.Name, badges...))
		if marker == ">" && marker != "*" {
			ui.Println(formatDetailLine(ui, "状态", "当前配置选择"))
		}
		ui.Println(formatDetailLine(ui, "home", profile.Home))
		ui.Println(formatDetailLine(ui, "auth", codex.AuthFilePath(profile.Home)))
	}
	return nil
}

func printCurrentPath(application *app.App) error {
	ui := uiForApp(application)
	ui.Println(ui.sectionTitle("Active path"))
	ui.Println(formatDetailLine(ui, "source", application.ActivePathSource))
	if application.ActivePathProfile != "" {
		ui.Println(formatDetailLine(ui, "profile", application.ActivePathProfile))
	}
	ui.Println(formatDetailLine(ui, "home", application.ActiveHome))
	ui.Println(formatDetailLine(ui, "auth", application.ActiveAuthPath))
	ui.Println(formatDetailLine(ui, "config", application.ActiveConfigPath))
	return nil
}

func handleAdd(application *app.App, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "覆盖同名账号")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("用法: codex-switch add [名称] [--force]")
	}

	name := ""
	if fs.NArg() == 1 {
		name = fs.Arg(0)
	}

	record, err := application.AddCurrent(name, *force)
	if err != nil {
		return err
	}
	printAccountSnapshot(uiForApp(application), fmt.Sprintf("已保存账号 %q", record.Name), record, nil)
	return nil
}

func handleLogin(application *app.App, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "覆盖同名账号")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("用法: codex-switch login [名称] [--force]")
	}

	name := ""
	if fs.NArg() == 1 {
		name = fs.Arg(0)
	}

	record, err := loginAccount(application, name, *force)
	if err != nil {
		return err
	}
	printAccountSnapshot(uiForApp(application), fmt.Sprintf("已登录并保存账号 %q", record.Name), record, nil)
	return nil
}

func handleImport(application *app.App, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "覆盖同名账号")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("用法: codex-switch import [名称] <auth.json> [--force]")
	}

	name := ""
	authPath := ""
	if fs.NArg() == 1 {
		authPath = fs.Arg(0)
	} else {
		name = fs.Arg(0)
		authPath = fs.Arg(1)
	}

	record, err := application.AddFromFile(name, authPath, *force)
	if err != nil {
		return err
	}
	printAccountSnapshot(uiForApp(application), fmt.Sprintf("已导入账号 %q", record.Name), record, nil)
	return nil
}

func handleList(application *app.App, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("用法: codex-switch list")
	}

	records, err := application.List()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		uiForApp(application).Println(uiForApp(application).sectionTitle("还没有保存任何账号，先执行 `codex-switch add <名称>`。"))
		return nil
	}

	current, err := application.Current()
	if err != nil {
		return err
	}

	sort.Slice(records, func(i, j int) bool {
		return strings.ToLower(records[i].Name) < strings.ToLower(records[j].Name)
	})

	ui := uiForApp(application)
	ui.Println(ui.sectionTitle("已保存账号"))
	for index, record := range records {
		if index > 0 {
			ui.Println("")
		}
		currentRecord := current.Managed != nil && strings.EqualFold(current.Managed.Name, record.Name)
		printAccountRecordCard(ui, record, currentRecord, [][2]string{
			{"类型", displayOr(record.Snapshot.AuthMode, "unknown")},
			{"保存时间", record.SavedAt.Local().Format("2006-01-02 15:04:05")},
		})
		if !record.LastSwitchedAt.IsZero() {
			ui.Println(formatDetailLine(ui, "最近切换", record.LastSwitchedAt.Local().Format("2006-01-02 15:04:05")))
		}
	}

	return nil
}

func handleCurrent(application *app.App) error {
	status, err := application.Current()
	if err != nil {
		return err
	}
	ui := uiForApp(application)
	if status.Live == nil {
		ui.Println(ui.sectionTitle("当前活动 auth path 中没有 auth.json。"))
		ui.Println(formatDetailLine(ui, "Auth path", application.ActiveAuthPath))
		return nil
	}

	ui.Println(ui.sectionTitle("当前活动账号"))
	liveTitle := displayOr(status.Live.AccountName, "当前账号")
	badges := []string{planBadge(ui, status.Live.Plan)}
	if status.Managed != nil {
		badges = append(badges, ui.badge("saved", "success"))
	} else {
		badges = append(badges, ui.badge("untracked", "warning"))
	}
	ui.Println(formatItemHeader(ui, status.Managed != nil, liveTitle, badges...))
	if status.Live.AccountName != "" {
		ui.Println(formatDetailLine(ui, "账户名", status.Live.AccountName))
	}
	if status.Live.Email != "" {
		ui.Println(formatDetailLine(ui, "邮箱", status.Live.Email))
	}
	ui.Println(formatDetailLine(ui, "认证类型", displayOr(status.Live.AuthMode, "unknown")))
	if status.Live.AccountID != "" {
		ui.Println(formatDetailLine(ui, "account_id", status.Live.AccountID))
	}
	if !status.Live.ExpiresAt.IsZero() {
		ui.Println(formatDetailLine(ui, "Token 过期", status.Live.ExpiresAt.Local().Format("2006-01-02 15:04:05")))
	}
	ui.Println(formatDetailLine(ui, "Home", application.ActiveHome))
	ui.Println(formatDetailLine(ui, "Auth path", application.ActiveAuthPath))
	ui.Println(formatDetailLine(ui, "Config path", application.ActiveConfigPath))
	if status.Managed != nil {
		ui.Println(formatDetailLine(ui, "已匹配账号", status.Managed.Name))
	} else {
		ui.Println(formatDetailLine(ui, "状态", "未匹配到已保存账号"))
	}
	return nil
}

func handleStatus(application *app.App, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("用法: codex-switch status")
	}

	statuses, err := application.Status()
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		uiForApp(application).Println(uiForApp(application).sectionTitle("还没有保存任何账号，先执行 `codex-switch add`。"))
		return nil
	}

	sort.Slice(statuses, func(i, j int) bool {
		return strings.ToLower(statuses[i].Record.Name) < strings.ToLower(statuses[j].Record.Name)
	})

	ui := uiForApp(application)
	ui.Println(ui.sectionTitle("账号额度"))
	for index, item := range statuses {
		if index > 0 {
			ui.Println("")
		}
		badges := []string{planBadge(ui, item.Record.Snapshot.Plan), aliasBadge(ui, item.Record.Name)}
		if item.Current {
			badges = append(badges, ui.badge("current", "success"))
		}
		if item.Refreshed {
			badges = append(badges, ui.badge("token refreshed", "accent"))
		}
		ui.Println(formatItemHeader(ui, item.Current, item.Record.Name, badges...))
		if item.Record.Snapshot.AccountName != "" && !strings.EqualFold(item.Record.Snapshot.AccountName, item.Record.Name) {
			ui.Println(formatDetailLine(ui, "账户名", item.Record.Snapshot.AccountName))
		}
		if item.Record.Snapshot.Email != "" {
			ui.Println(formatDetailLine(ui, "邮箱", item.Record.Snapshot.Email))
		}
		if item.Error != "" {
			ui.Println(formatDetailLine(ui, "状态", ui.danger(item.Error)))
			continue
		}
		hourly := formatQuotaValue(ui, item.Quota.Hourly.RemainingPercent)
		if !item.Quota.Hourly.ResetAt.IsZero() {
			hourly += "  " + ui.muted("重置 "+item.Quota.Hourly.ResetAt.Local().Format("2006-01-02 15:04:05"))
		}
		ui.Println(formatDetailLine(ui, "5小时额度", hourly))
		weekly := formatQuotaValue(ui, item.Quota.Weekly.RemainingPercent)
		if !item.Quota.Weekly.ResetAt.IsZero() {
			weekly += "  " + ui.muted("重置 "+item.Quota.Weekly.ResetAt.Local().Format("2006-01-02 15:04:05"))
		}
		ui.Println(formatDetailLine(ui, "周额度", weekly))
	}
	return nil
}

func handleSwitch(application *app.App, args []string) error {
	loginMode := false
	force := false
	positionals := make([]string, 0, 1)

	for _, arg := range args {
		switch arg {
		case "--login":
			loginMode = true
		case "--force":
			force = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("未知参数: %s\n用法: codex-switch switch <名称>\n      codex-switch switch [--login] [--force] <名称>", arg)
			}
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 && !loginMode {
		selectedName, err := promptSwitchSelection(application)
		if err != nil {
			return err
		}
		positionals = append(positionals, selectedName)
	}
	if len(positionals) != 1 {
		return fmt.Errorf("用法: codex-switch switch\n      codex-switch switch <名称>\n      codex-switch switch [--login] [--force] <名称>")
	}

	name := positionals[0]
	if loginMode {
		record, err := loginAccount(application, name, force)
		if err != nil {
			return err
		}
		printAccountSnapshot(uiForApp(application), fmt.Sprintf("已登录并保存账号 %q", record.Name), record, nil)
		return nil
	}

	record, backupPath, err := switchAccount(application, name)
	if err != nil {
		record, handled, promptErr := maybePromptSwitchLogin(application, err, name, force)
		if handled {
			if promptErr != nil {
				return promptErr
			}
			printAccountSnapshot(uiForApp(application), fmt.Sprintf("已登录并保存账号 %q", record.Name), record, nil)
			return nil
		}
		return withSwitchLoginHint(err, name)
	}
	extras := [][2]string{{"目标 auth", application.ActiveAuthPath}}
	if backupPath != "" {
		extras = append(extras, [2]string{"切换前备份", backupPath})
	}
	printAccountSnapshot(uiForApp(application), fmt.Sprintf("已切换到账号 %q", record.Name), record, extras)
	return nil
}

func handleRemove(application *app.App, args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := ""
	switch fs.NArg() {
	case 0:
		selectedName, err := promptAccountSelection(application, "请选择要删除的账号:")
		if err != nil {
			return err
		}
		name = selectedName
		if ok, err := confirmAccountAction(application, fmt.Sprintf("确认删除账号 %q 吗？ [y/N]: ", name)); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("已取消删除")
		}
	case 1:
		name = fs.Arg(0)
	default:
		return fmt.Errorf("用法: codex-switch remove\n      codex-switch remove <名称>")
	}

	if err := application.Remove(name); err != nil {
		return err
	}
	uiForApp(application).Println(uiForApp(application).sectionTitle(fmt.Sprintf("已删除账号 %q", name)))
	return nil
}

func handleRename(application *app.App, args []string) error {
	fs := flag.NewFlagSet("rename", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	from := fs.String("from", "", "原名称")
	to := fs.String("to", "", "新名称")
	if err := fs.Parse(args); err != nil {
		return err
	}

	oldName := strings.TrimSpace(*from)
	newName := strings.TrimSpace(*to)
	switch {
	case oldName != "" || newName != "":
		if fs.NArg() != 0 {
			return fmt.Errorf("用法: codex-switch rename <旧名称> <新名称>\n      codex-switch rename --from <旧名称> --to <新名称>")
		}
	default:
		switch fs.NArg() {
		case 0:
			selectedName, err := promptAccountSelection(application, "请选择要重命名的账号:")
			if err != nil {
				return err
			}
			oldName = selectedName
		case 1:
			oldName = fs.Arg(0)
		case 2:
			oldName = fs.Arg(0)
			newName = fs.Arg(1)
		default:
			return fmt.Errorf("用法: codex-switch rename <旧名称> <新名称>\n      codex-switch rename --from <旧名称> --to <新名称>")
		}
	}

	if strings.TrimSpace(oldName) == "" {
		return fmt.Errorf("用法: codex-switch rename <旧名称> <新名称>\n      codex-switch rename --from <旧名称> --to <新名称>")
	}
	if strings.TrimSpace(newName) == "" {
		input, err := promptTextInput(application, fmt.Sprintf("请输入账号 %q 的新名称，直接回车取消: ", oldName))
		if err != nil {
			return err
		}
		newName = input
	}

	record, err := application.Rename(oldName, newName)
	if err != nil {
		return err
	}
	ui := uiForApp(application)
	ui.Println(ui.sectionTitle(fmt.Sprintf("已将账号 %q 重命名为 %q", oldName, record.Name)))
	printAccountRecordCard(ui, *record, false, nil)
	return nil
}

func handleExport(application *app.App, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	name := ""
	targetPath := ""
	switch fs.NArg() {
	case 0:
		selectedName, err := promptAccountSelection(application, "请选择要导出的账号:")
		if err != nil {
			return err
		}
		name = selectedName
	case 1:
		name = fs.Arg(0)
	case 2:
		name = fs.Arg(0)
		targetPath = fs.Arg(1)
	default:
		return fmt.Errorf("用法: codex-switch export <名称> <auth.json>")
	}

	if strings.TrimSpace(targetPath) == "" {
		input, err := promptTextInputWithDefaults(application, fmt.Sprintf("请选择账号 %q 的导出 auth.json 路径", name), defaultExportAuthPaths(application, name))
		if err != nil {
			return err
		}
		targetPath = input
	}

	record, targetPath, err := exportAuthAccount(application, name, targetPath)
	if err != nil {
		return err
	}
	ui := uiForApp(application)
	ui.Println(ui.sectionTitle(fmt.Sprintf("已将账号 %q 导出到 %s", record.Name, targetPath)))
	printAccountRecordCard(ui, *record, false, exportPathExtras(ui, "导出路径", targetPath, "打开文件"))
	return nil
}

func handleExportHome(application *app.App, args []string) error {
	fs := flag.NewFlagSet("export-home", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	copyConfig := fs.Bool("copy-config", true, "复制当前 config.toml")
	noCopyConfig := fs.Bool("no-copy-config", false, "不复制当前 config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *noCopyConfig {
		*copyConfig = false
	}

	name := ""
	dir := ""
	switch fs.NArg() {
	case 0:
		selectedName, err := promptAccountSelection(application, "请选择要导出 home 的账号:")
		if err != nil {
			return err
		}
		name = selectedName
	case 1:
		name = fs.Arg(0)
	case 2:
		name = fs.Arg(0)
		dir = fs.Arg(1)
	default:
		return fmt.Errorf("用法: codex-switch export-home <名称> <目录> [--no-copy-config]")
	}

	if strings.TrimSpace(dir) == "" {
		input, err := promptTextInputWithDefaults(application, fmt.Sprintf("请选择账号 %q 的导出目录", name), defaultExportHomePaths(application, name))
		if err != nil {
			return err
		}
		dir = input
	}

	record, targetHome, err := exportHomeAccount(application, name, dir, *copyConfig)
	if err != nil {
		return err
	}

	ui := uiForApp(application)
	ui.Println(ui.sectionTitle(fmt.Sprintf("已导出账号 %q 到 %s", record.Name, targetHome)))
	printAccountRecordCard(ui, *record, false, exportPathExtras(ui, "导出 Home", targetHome, "打开目录"))
	ui.Println("")
	ui.Println(ui.sectionTitle("可用以下方式启动隔离实例"))
	ui.Println(formatDetailLine(ui, "Step 1", shellEnvSetCommand(runtime.GOOS, "CODEX_HOME", targetHome)))
	ui.Println(formatDetailLine(ui, "Step 2", "codex"))
	return nil
}

func shellEnvSetCommand(goos, key, value string) string {
	if goos == "windows" {
		return fmt.Sprintf(`$env:%s="%s"`, key, value)
	}
	return fmt.Sprintf(`export %s="%s"`, key, value)
}

func handleRun(application *app.App, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", "", "指定隔离 home 目录")
	cd := fs.String("cd", "", "启动前切换到该目录")
	copyConfig := fs.Bool("copy-config", true, "复制当前 config.toml")
	noCopyConfig := fs.Bool("no-copy-config", false, "不复制当前 config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *noCopyConfig {
		*copyConfig = false
	}

	name := ""
	codexArgs := fs.Args()
	if len(codexArgs) > 0 {
		name = codexArgs[0]
		codexArgs = codexArgs[1:]
	} else {
		selectedName, err := promptAccountSelection(application, "请选择要运行的账号:")
		if err != nil {
			return err
		}
		name = selectedName
	}

	opts := app.RunOptions{
		HomeDir:    *home,
		WorkingDir: *cd,
		CopyConfig: *copyConfig,
		Args:       codexArgs,
	}
	targetHome, err := runAccount(application, name, opts)
	if err != nil {
		return err
	}

	ui := uiForApp(application)
	ui.Println(ui.sectionTitle("已使用隔离 home 启动 Codex"))
	ui.Println(formatDetailLine(ui, "Home", targetHome))
	return nil
}

func displayOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultExportBaseName(name string) string {
	base := store.ShellName(name)
	if strings.TrimSpace(base) == "" {
		return "codex-account"
	}
	return base
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		key := strings.ToLower(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, path)
	}
	return result
}

func defaultExportAuthPaths(application *app.App, name string) []string {
	base := defaultExportBaseName(name)
	paths := []string{
		filepath.Join("exports", base+"-auth.json"),
	}
	if application != nil && application.Store != nil {
		paths = append(paths, filepath.Join(application.Store.Root(), "exports", base+"-auth.json"))
	}
	return uniquePaths(paths)
}

func defaultExportHomePaths(application *app.App, name string) []string {
	base := "codex-" + defaultExportBaseName(name)
	paths := []string{
		filepath.Join("exports", base),
	}
	if application != nil && application.Store != nil {
		paths = append(paths, filepath.Join(application.Store.Root(), "exports", base))
	}
	return uniquePaths(paths)
}

func exportAuthPathSuggestionItems(application *app.App, name string) []realtimeSuggestionItem {
	paths := defaultExportAuthPaths(application, name)
	if len(paths) == 0 {
		return nil
	}
	items := make([]realtimeSuggestionItem, 0, len(paths))
	for index, path := range paths {
		summary := "默认导出 auth.json 路径"
		switch index {
		case 0:
			summary = "默认: 当前目录 exports"
		case 1:
			summary = "默认: codex-switch store exports"
		}
		items = append(items, realtimeSuggestionItem{
			Group:   "paths",
			Value:   path,
			Summary: summary,
		})
	}
	return items
}

func exportHomePathSuggestionItems(application *app.App, name string) []realtimeSuggestionItem {
	paths := defaultExportHomePaths(application, name)
	if len(paths) == 0 {
		return nil
	}
	items := make([]realtimeSuggestionItem, 0, len(paths))
	for index, path := range paths {
		summary := "默认导出 home 路径"
		switch index {
		case 0:
			summary = "默认: 当前目录 exports"
		case 1:
			summary = "默认: codex-switch store exports"
		}
		items = append(items, realtimeSuggestionItem{
			Group:   "paths",
			Value:   path,
			Summary: summary,
		})
	}
	return items
}

func promptTextInputWithDefaults(application *app.App, prompt string, defaults []string) (string, error) {
	if application == nil || application.Stdin == nil {
		return "", fmt.Errorf("缺少交互输入")
	}

	defaults = uniquePaths(defaults)
	if len(defaults) == 0 {
		return promptTextInput(application, prompt)
	}

	writer := io.Writer(os.Stdout)
	if application.Stdout != nil {
		writer = application.Stdout
	}
	ui := newTerminalUI(writer)
	if _, err := fmt.Fprintln(writer, ui.sectionTitle(prompt)); err != nil {
		return "", err
	}
	for index, path := range defaults {
		tag := ui.badge("default", "accent")
		if index == 0 {
			tag = ui.badge("recommended", "success")
		}
		if _, err := fmt.Fprintf(writer, "  %d. %s %s\n", index+1, ui.strong(path), tag); err != nil {
			return "", err
		}
	}
	if _, err := fmt.Fprintf(writer, "%s 回车用默认值，也可输入序号、自定义路径、/cancel 取消或 /exit 退出: ", ui.accent(">")); err != nil {
		return "", err
	}

	line, readErr := readPromptLine(application)
	if readErr != nil {
		return "", readErr
	}
	switch promptControlAction(line) {
	case "exit":
		return "", errExitInteractiveShell
	case "cancel":
		return "", fmt.Errorf("已取消输入")
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return defaults[0], nil
	}
	if index, err := parseSelectionIndex(value, len(defaults)); err == nil {
		return defaults[index], nil
	}
	return value, nil
}

func absolutePathOrOriginal(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return abs
}

func localFileURI(path string) string {
	abs := absolutePathOrOriginal(path)
	if abs == "" {
		return ""
	}
	slashPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func exportPathExtras(ui terminalUI, label, path, linkLabel string) [][2]string {
	abs := absolutePathOrOriginal(path)
	actualValue := abs
	linkValue := ""
	if abs != "" {
		actualValue = ui.link(abs, localFileURI(abs))
		linkValue = ui.link(linkLabel, localFileURI(abs))
	}
	extras := [][2]string{
		{label, path},
		{"实际路径", actualValue},
	}
	if strings.TrimSpace(linkValue) != "" {
		extras = append(extras, [2]string{"本地链接", linkValue})
	}
	return extras
}

func printAccountSnapshot(ui terminalUI, title string, record *store.Record, extras [][2]string) {
	if record == nil {
		return
	}
	ui.Println(ui.sectionTitle(title))
	printAccountRecordCard(ui, *record, false, extras)
}

func printAccountRecordCard(ui terminalUI, record store.Record, current bool, extras [][2]string) {
	badges := []string{planBadge(ui, record.Snapshot.Plan), aliasBadge(ui, record.Name)}
	if current {
		badges = append(badges, ui.badge("current", "success"))
	}
	ui.Println(formatItemHeader(ui, current, record.Name, badges...))
	if record.Snapshot.AccountName != "" && !strings.EqualFold(record.Snapshot.AccountName, record.Name) {
		ui.Println(formatDetailLine(ui, "账户名", record.Snapshot.AccountName))
	}
	if record.Snapshot.Email != "" {
		ui.Println(formatDetailLine(ui, "邮箱", record.Snapshot.Email))
	}
	if record.Snapshot.AccountID != "" {
		ui.Println(formatDetailLine(ui, "account_id", record.Snapshot.AccountID))
	}
	for _, extra := range extras {
		if len(extra) < 2 || strings.TrimSpace(extra[1]) == "" {
			continue
		}
		ui.Println(formatDetailLine(ui, extra[0], extra[1]))
	}
}

func maybePromptSwitchLogin(application *app.App, err error, name string, force bool) (*store.Record, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" || err == nil || !isMissingAccountError(err, name) {
		return nil, false, nil
	}

	if application == nil || application.Stdin == nil {
		return nil, false, nil
	}

	writer := io.Writer(os.Stdout)
	if application.Stdout != nil {
		writer = application.Stdout
	}
	ui := newTerminalUI(writer)
	if _, writeErr := fmt.Fprintf(writer, "%s %q 还未保存，是否现在登录并保存？ %s: ", ui.accent("? 账号"), name, ui.muted("[y/N, /cancel, /exit]")); writeErr != nil {
		return nil, false, nil
	}

	line, readErr := readPromptLine(application)
	if readErr != nil {
		return nil, false, nil
	}
	switch promptControlAction(line) {
	case "exit":
		return nil, true, errExitInteractiveShell
	case "cancel":
		return nil, true, fmt.Errorf("已取消登录")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return nil, false, nil
	}

	record, loginErr := loginAccount(application, name, force)
	if loginErr != nil {
		return nil, true, loginErr
	}
	return record, true, nil
}

func promptSwitchSelection(application *app.App) (string, error) {
	return promptAccountSelection(application, "请选择要切换的账号:")
}

func promptAccountSelection(application *app.App, header string) (string, error) {
	if application == nil || application.Stdin == nil {
		return "", fmt.Errorf("请提供要切换的账号名称")
	}

	records, err := application.List()
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", fmt.Errorf("还没有保存任何账号，先执行 `codex-switch add`。")
	}

	current, err := application.Current()
	if err != nil {
		return "", err
	}

	sort.Slice(records, func(i, j int) bool {
		return strings.ToLower(records[i].Name) < strings.ToLower(records[j].Name)
	})

	writer := io.Writer(os.Stdout)
	if application.Stdout != nil {
		writer = application.Stdout
	}
	ui := newTerminalUI(writer)

	if _, err := fmt.Fprintln(writer, ui.sectionTitle(header)); err != nil {
		return "", err
	}
	for index, record := range records {
		currentRecord := current.Managed != nil && strings.EqualFold(current.Managed.Name, record.Name)
		badges := []string{planBadge(ui, record.Snapshot.Plan), aliasBadge(ui, record.Name)}
		if currentRecord {
			badges = append(badges, ui.badge("current", "success"))
		}
		if _, err := fmt.Fprintf(writer, "  %d. %s\n", index+1, formatItemHeader(ui, currentRecord, record.Name, badges...)); err != nil {
			return "", err
		}
		if record.Snapshot.Email != "" {
			if _, err := fmt.Fprintln(writer, "     "+formatDetailLine(ui, "邮箱", record.Snapshot.Email)); err != nil {
				return "", err
			}
		}
		if record.Snapshot.AccountName != "" && !strings.EqualFold(record.Snapshot.AccountName, record.Name) {
			if _, err := fmt.Fprintln(writer, "     "+formatDetailLine(ui, "账户名", record.Snapshot.AccountName)); err != nil {
				return "", err
			}
		}
		if index < len(records)-1 {
			if _, err := fmt.Fprintln(writer); err != nil {
				return "", err
			}
		}
	}
	if _, err := fmt.Fprintf(writer, "%s ", ui.accent(">")); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(writer, "输入序号或名称，回车或 /cancel 取消，/exit 退出: "); err != nil {
		return "", err
	}

	line, readErr := readPromptLine(application)
	if readErr != nil {
		return "", readErr
	}
	switch promptControlAction(line) {
	case "exit":
		return "", errExitInteractiveShell
	case "cancel":
		return "", fmt.Errorf("已取消切换")
	}
	input := strings.TrimSpace(line)
	if input == "" {
		return "", fmt.Errorf("已取消切换")
	}

	if index, parseErr := parseSelectionIndex(input, len(records)); parseErr == nil {
		return records[index].Name, nil
	}
	return input, nil
}

func confirmAccountAction(application *app.App, prompt string) (bool, error) {
	if application == nil || application.Stdin == nil {
		return false, nil
	}

	writer := io.Writer(os.Stdout)
	if application.Stdout != nil {
		writer = application.Stdout
	}
	ui := newTerminalUI(writer)
	if _, err := fmt.Fprintf(writer, "%s %s", ui.accent("?"), strings.TrimRight(prompt, " ")); err != nil {
		return false, err
	}
	if _, err := fmt.Fprint(writer, ui.muted(" [输入 /cancel 取消，/exit 退出]: ")); err != nil {
		return false, err
	}

	line, readErr := readPromptLine(application)
	if readErr != nil {
		return false, readErr
	}
	switch promptControlAction(line) {
	case "exit":
		return false, errExitInteractiveShell
	case "cancel":
		return false, fmt.Errorf("已取消操作")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func promptTextInput(application *app.App, prompt string) (string, error) {
	if application == nil || application.Stdin == nil {
		return "", fmt.Errorf("缺少交互输入")
	}

	writer := io.Writer(os.Stdout)
	if application.Stdout != nil {
		writer = application.Stdout
	}
	ui := newTerminalUI(writer)
	if _, err := fmt.Fprintf(writer, "%s %s", ui.accent(">"), strings.TrimRight(prompt, " ")); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(writer, ui.muted(" (/cancel 取消，/exit 退出): ")); err != nil {
		return "", err
	}

	line, readErr := readPromptLine(application)
	if readErr != nil {
		return "", readErr
	}
	switch promptControlAction(line) {
	case "exit":
		return "", errExitInteractiveShell
	case "cancel":
		return "", fmt.Errorf("已取消重命名")
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", fmt.Errorf("已取消重命名")
	}
	return value, nil
}

func inputReader(application *app.App) *bufio.Reader {
	if application == nil || application.Stdin == nil {
		return bufio.NewReader(strings.NewReader(""))
	}
	if reader, ok := application.Stdin.(*bufio.Reader); ok {
		return reader
	}
	if application.InputReader == nil {
		application.InputReader = bufio.NewReader(application.Stdin)
	}
	return application.InputReader
}

func parseSelectionIndex(input string, count int) (int, error) {
	selected, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil {
		return 0, err
	}
	selected--
	if selected < 0 || selected >= count {
		return 0, fmt.Errorf("invalid selection")
	}
	return selected, nil
}

func withSwitchLoginHint(err error, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || err == nil {
		return err
	}

	if isMissingAccountError(err, name) {
		return fmt.Errorf("%w\n未保存账号 %q，可改用: codex-switch switch --login %q", err, name, name)
	}
	return err
}

func isMissingAccountError(err error, name string) bool {
	notFoundMessage := fmt.Sprintf("账号 %q 不存在", strings.TrimSpace(name))
	return err != nil && strings.Contains(err.Error(), notFoundMessage)
}

func buildRecordLabel(record store.Record) string {
	label := strings.TrimSpace(record.Name)
	accountName := strings.TrimSpace(record.Snapshot.AccountName)
	email := strings.TrimSpace(record.Snapshot.Email)

	if accountName != "" && !strings.EqualFold(accountName, label) {
		label += " [" + accountName + "]"
	}
	if email != "" {
		label += " (" + email + ")"
	}
	return label
}

var _ = store.Record{}
