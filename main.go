package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
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
		return runInteractiveShell(application)
	}
	return runWithApp(application, args)
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
  支持命令前缀匹配，例如 /sw、/st、/ren。
  当前缀匹配到多个命令时，会动态展示候选列表并支持按序号或命令名选择。
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
	writer := io.Writer(os.Stdout)
	if application.Stdout != nil {
		writer = application.Stdout
	}
	if _, err := fmt.Fprintln(writer, "codex-switch shell"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "Commands start with /. Type / to browse commands, /help for help, and /exit to quit."); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Context: %s\n", interactiveShellContext(application)); err != nil {
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

	state, err := term.MakeRaw(int(inputFile.Fd()))
	if err != nil {
		return runLineInteractiveShell(application, outputFile)
	}
	defer func() {
		_ = term.Restore(int(inputFile.Fd()), state)
		fmt.Fprint(outputFile, "\r\n")
	}()

	buffer := make([]byte, 0, 128)
	renderedLines := 0
	selectedSuggestion := 0
	suggestionsVisible := true
	for {
		renderedLines, selectedSuggestion, err = renderInteractiveInput(outputFile, renderedLines, application, interactiveShellPrompt(application), string(buffer), selectedSuggestion, suggestionsVisible)
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
			fmt.Fprint(outputFile, "\r\n")
			renderedLines = 0
			line := strings.TrimSpace(string(buffer))
			buffer = buffer[:0]
			selectedSuggestion = 0
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
			selectedSuggestion = 0
			suggestionsVisible = true
		case '\t':
			completed := autocompleteInteractiveLine(application, string(buffer), selectedSuggestion)
			if completed != string(buffer) {
				buffer = []byte(completed)
			}
			selectedSuggestion = 0
			suggestionsVisible = true
		case 27:
			action := readEscapeAction(inputFile)
			switch action {
			case "up":
				suggestionsVisible = true
				selectedSuggestion = moveSuggestionSelectionWithApp(application, string(buffer), selectedSuggestion, -1)
			case "down":
				suggestionsVisible = true
				selectedSuggestion = moveSuggestionSelectionWithApp(application, string(buffer), selectedSuggestion, 1)
			case "right":
				completed := autocompleteInteractiveLine(application, string(buffer), selectedSuggestion)
				if completed != string(buffer) {
					buffer = []byte(completed)
				}
				suggestionsVisible = true
				selectedSuggestion = 0
			case "escape":
				suggestionsVisible = false
				selectedSuggestion = -1
			}
		default:
			if b[0] >= 32 && b[0] != 255 {
				buffer = append(buffer, b[0])
				suggestionsVisible = true
				selectedSuggestion = 0
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

	if runErr := runCommand(application, args, true); runErr != nil {
		_, _ = fmt.Fprintf(writer, "错误: %v\n", runErr)
	}
	return false, nil
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

func renderInteractiveInput(writer io.Writer, previousLines int, application *app.App, prompt, line string, selected int, visible bool) (int, int, error) {
	suggestions := interactiveRealtimeSuggestions(application, line)
	if !visible {
		suggestions = realtimeCommandSuggestions{}
	}
	selected = normalizeSuggestionSelection(len(suggestions.Matches), selected)
	if previousLines > 0 {
		if _, err := fmt.Fprintf(writer, "\x1b[%dA", previousLines); err != nil {
			return 0, selected, err
		}
	}
	if _, err := fmt.Fprint(writer, "\r\x1b[J"); err != nil {
		return 0, selected, err
	}

	renderedLines := 0
	for _, suggestion := range renderInteractiveSuggestionLines(suggestions, selected) {
		if _, err := fmt.Fprintln(writer, suggestion); err != nil {
			return 0, selected, err
		}
		renderedLines++
	}
	if _, err := fmt.Fprintf(writer, "%s%s", prompt, line); err != nil {
		return 0, selected, err
	}
	return renderedLines, selected, nil
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
	return fmt.Sprintf("codex-switch[%s]> ", interactiveShellContext(application))
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
	var builder strings.Builder
	builder.WriteString("Available commands:\n")
	for _, command := range interactiveShellCommands {
		aliasText := ""
		if len(command.Aliases) > 0 {
			prefixedAliases := make([]string, 0, len(command.Aliases))
			for _, alias := range command.Aliases {
				prefixedAliases = append(prefixedAliases, "/"+alias)
			}
			aliasText = " [" + strings.Join(prefixedAliases, ", ") + "]"
		}
		builder.WriteString(fmt.Sprintf("  %-12s %-20s %s\n", "/"+command.Name, aliasText, command.Summary))
	}
	builder.WriteString("Tip: 输入 / 查看全部命令，输入 /<前缀> 匹配命令。")
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

func renderInteractiveSuggestionLines(suggestions realtimeCommandSuggestions, selected int) []string {
	if strings.TrimSpace(suggestions.Hint) != "" {
		return []string{suggestions.Hint}
	}

	lines := make([]string, 0, len(suggestions.Matches)+1)
	lines = append(lines, "suggestions:")
	lastGroup := ""
	for index, suggestion := range suggestions.Matches {
		if suggestion.Group != "" && suggestion.Group != lastGroup {
			lines = append(lines, "  ["+suggestion.Group+"]")
			lastGroup = suggestion.Group
		}
		prefix := "  "
		if index == selected {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%-18s %s", prefix, suggestion.Value, suggestion.Summary)
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

	selected = normalizeSuggestionSelection(len(suggestions.Matches), selected)
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
		if len(positionals) >= 1 && hasTrailingSpace {
			return realtimeCommandSuggestions{Hint: "hint: /export-home <account> <dir> [--no-copy-config]"}
		}
	case "export":
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

func resolveInteractiveCommand(application *app.App, input string) (string, error) {
	query := strings.ToLower(strings.TrimSpace(input))
	if query == "" {
		return "help", nil
	}

	if exact, ok := findInteractiveCommandExact(query); ok {
		return exact.Name, nil
	}

	matches := findInteractiveCommandMatches(query)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("未知命令 %q。输入 /help 查看可用命令", "/"+input)
	case 1:
		return matches[0].Name, nil
	default:
		return promptCommandSelection(application, query, matches)
	}
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

	reader := inputReader(application)
	for {
		if _, err := fmt.Fprint(writer, "输入序号或命令名，直接回车取消: "); err != nil {
			return "", err
		}

		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return "", readErr
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
		fmt.Printf("已保存 path profile %q\n", profile.Name)
		fmt.Printf("Home: %s\n", profile.Home)
		fmt.Printf("Auth: %s\n", codex.AuthFilePath(profile.Home))
		if *use {
			fmt.Printf("已设为当前 path profile\n")
			if application.ActivePathSource == "env" {
				fmt.Printf("当前会话仍优先使用 CODEX_HOME=%s\n", application.ActiveHome)
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
		fmt.Printf("已切换到 path profile %q\n", profile.Name)
		fmt.Printf("Home: %s\n", profile.Home)
		fmt.Printf("Auth: %s\n", codex.AuthFilePath(profile.Home))
		if application.ActivePathSource == "env" {
			fmt.Printf("当前会话仍优先使用 CODEX_HOME=%s\n", application.ActiveHome)
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
		fmt.Printf("已删除 path profile %q\n", positionals[0])
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
		fmt.Printf("已清除当前 path profile 选择\n")
		return printCurrentPath(application)
	default:
		return fmt.Errorf("未知 paths 子命令: %s", subcommand)
	}
}

func printPathsOverview(application *app.App) error {
	if err := printCurrentPath(application); err != nil {
		return err
	}
	fmt.Println()
	fmt.Printf("Store root: %s\n", application.Store.Root())
	fmt.Printf("Accounts:   %s\n", application.Store.AccountsDir())
	fmt.Printf("Homes:      %s\n", application.Store.HomesDir())
	fmt.Printf("Backups:    %s\n", application.Store.BackupsDir())
	fmt.Printf("Profiles:   %s\n", application.Store.PathsConfigPath())

	profiles, currentProfile, err := application.Store.ListPathProfiles()
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Path profiles:")
	if len(profiles) == 0 {
		fmt.Println("  (none)")
		return nil
	}

	for _, profile := range profiles {
		marker := " "
		switch {
		case application.ActivePathSource == "profile" && strings.EqualFold(application.ActivePathProfile, profile.Name):
			marker = "*"
		case currentProfile != "" && strings.EqualFold(currentProfile, profile.Name):
			marker = ">"
		}
		fmt.Printf("  %s %s\n", marker, profile.Name)
		fmt.Printf("    home: %s\n", profile.Home)
		fmt.Printf("    auth: %s\n", codex.AuthFilePath(profile.Home))
	}
	return nil
}

func printCurrentPath(application *app.App) error {
	fmt.Println("Active path:")
	fmt.Printf("  source: %s\n", application.ActivePathSource)
	if application.ActivePathProfile != "" {
		fmt.Printf("  profile: %s\n", application.ActivePathProfile)
	}
	fmt.Printf("  home: %s\n", application.ActiveHome)
	fmt.Printf("  auth: %s\n", application.ActiveAuthPath)
	fmt.Printf("  config: %s\n", application.ActiveConfigPath)
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

	fmt.Printf("已保存账号 %q\n", record.Name)
	if record.Snapshot.AccountName != "" {
		fmt.Printf("账户名: %s\n", record.Snapshot.AccountName)
	}
	if record.Snapshot.Email != "" {
		fmt.Printf("邮箱: %s\n", record.Snapshot.Email)
	}
	if record.Snapshot.Plan != "" {
		fmt.Printf("套餐: %s\n", record.Snapshot.Plan)
	}
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

	fmt.Printf("已登录并保存账号 %q\n", record.Name)
	if record.Snapshot.AccountName != "" {
		fmt.Printf("账户名: %s\n", record.Snapshot.AccountName)
	}
	if record.Snapshot.Email != "" {
		fmt.Printf("邮箱: %s\n", record.Snapshot.Email)
	}
	if record.Snapshot.Plan != "" {
		fmt.Printf("套餐: %s\n", record.Snapshot.Plan)
	}
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
	fmt.Printf("已导入账号 %q\n", record.Name)
	if record.Snapshot.AccountName != "" {
		fmt.Printf("账户名: %s\n", record.Snapshot.AccountName)
	}
	if record.Snapshot.Email != "" {
		fmt.Printf("邮箱: %s\n", record.Snapshot.Email)
	}
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
		fmt.Println("还没有保存任何账号，先执行 `codex-switch add <名称>`。")
		return nil
	}

	current, err := application.Current()
	if err != nil {
		return err
	}

	sort.Slice(records, func(i, j int) bool {
		return strings.ToLower(records[i].Name) < strings.ToLower(records[j].Name)
	})

	for _, record := range records {
		marker := " "
		if current.Managed != nil && strings.EqualFold(current.Managed.Name, record.Name) {
			marker = "*"
		}

		label := buildRecordLabel(record)

		fmt.Printf("%s %s\n", marker, label)
		fmt.Printf("  类型: %s\n", displayOr(record.Snapshot.AuthMode, "unknown"))
		if record.Snapshot.AccountName != "" && !strings.EqualFold(record.Snapshot.AccountName, record.Name) {
			fmt.Printf("  账户名: %s\n", record.Snapshot.AccountName)
		}
		if record.Snapshot.Plan != "" {
			fmt.Printf("  套餐: %s\n", record.Snapshot.Plan)
		}
		if record.Snapshot.AccountID != "" {
			fmt.Printf("  account_id: %s\n", record.Snapshot.AccountID)
		}
		if shellName := store.ShellName(record.Name); shellName != "" && !strings.EqualFold(shellName, record.Name) {
			fmt.Printf("  命令别名: %s\n", shellName)
		}
		fmt.Printf("  保存时间: %s\n", record.SavedAt.Local().Format("2006-01-02 15:04:05"))
		if !record.LastSwitchedAt.IsZero() {
			fmt.Printf("  最近切换: %s\n", record.LastSwitchedAt.Local().Format("2006-01-02 15:04:05"))
		}
	}

	return nil
}

func handleCurrent(application *app.App) error {
	status, err := application.Current()
	if err != nil {
		return err
	}
	if status.Live == nil {
		fmt.Println("当前活动 auth path 中没有 auth.json。")
		fmt.Printf("Auth path: %s\n", application.ActiveAuthPath)
		return nil
	}

	fmt.Printf("当前活动 Home: %s\n", application.ActiveHome)
	fmt.Printf("Auth path: %s\n", application.ActiveAuthPath)
	fmt.Printf("Config path: %s\n", application.ActiveConfigPath)
	fmt.Printf("认证类型: %s\n", displayOr(status.Live.AuthMode, "unknown"))
	if status.Live.AccountName != "" {
		fmt.Printf("账户名: %s\n", status.Live.AccountName)
	}
	if status.Live.Email != "" {
		fmt.Printf("邮箱: %s\n", status.Live.Email)
	}
	if status.Live.Plan != "" {
		fmt.Printf("套餐: %s\n", status.Live.Plan)
	}
	if status.Live.AccountID != "" {
		fmt.Printf("account_id: %s\n", status.Live.AccountID)
	}
	if !status.Live.ExpiresAt.IsZero() {
		fmt.Printf("Token 过期时间: %s\n", status.Live.ExpiresAt.Local().Format("2006-01-02 15:04:05"))
	}
	if status.Managed != nil {
		fmt.Printf("已匹配到已保存账号: %s\n", status.Managed.Name)
	} else {
		fmt.Println("未匹配到已保存账号。")
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
		fmt.Println("还没有保存任何账号，先执行 `codex-switch add`。")
		return nil
	}

	sort.Slice(statuses, func(i, j int) bool {
		return strings.ToLower(statuses[i].Record.Name) < strings.ToLower(statuses[j].Record.Name)
	})

	for _, item := range statuses {
		marker := " "
		if item.Current {
			marker = "*"
		}
		fmt.Printf("%s %s\n", marker, buildRecordLabel(item.Record))
		if item.Record.Snapshot.Plan != "" {
			fmt.Printf("  套餐: %s\n", item.Record.Snapshot.Plan)
		}
		if item.Refreshed {
			fmt.Printf("  Token: 已自动刷新\n")
		}
		if item.Error != "" {
			fmt.Printf("  状态: %s\n", item.Error)
			continue
		}
		fmt.Printf("  5小时额度: %d%%", item.Quota.Hourly.RemainingPercent)
		if !item.Quota.Hourly.ResetAt.IsZero() {
			fmt.Printf("  重置: %s", item.Quota.Hourly.ResetAt.Local().Format("2006-01-02 15:04:05"))
		}
		fmt.Println()
		fmt.Printf("  周额度: %d%%", item.Quota.Weekly.RemainingPercent)
		if !item.Quota.Weekly.ResetAt.IsZero() {
			fmt.Printf("  重置: %s", item.Quota.Weekly.ResetAt.Local().Format("2006-01-02 15:04:05"))
		}
		fmt.Println()
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
		fmt.Printf("已登录并保存账号 %q\n", record.Name)
		if record.Snapshot.AccountName != "" {
			fmt.Printf("账户名: %s\n", record.Snapshot.AccountName)
		}
		if record.Snapshot.Email != "" {
			fmt.Printf("邮箱: %s\n", record.Snapshot.Email)
		}
		if record.Snapshot.Plan != "" {
			fmt.Printf("套餐: %s\n", record.Snapshot.Plan)
		}
		return nil
	}

	record, backupPath, err := switchAccount(application, name)
	if err != nil {
		record, handled, promptErr := maybePromptSwitchLogin(application, err, name, force)
		if handled {
			if promptErr != nil {
				return promptErr
			}
			fmt.Printf("已登录并保存账号 %q\n", record.Name)
			if record.Snapshot.AccountName != "" {
				fmt.Printf("账户名: %s\n", record.Snapshot.AccountName)
			}
			if record.Snapshot.Email != "" {
				fmt.Printf("邮箱: %s\n", record.Snapshot.Email)
			}
			if record.Snapshot.Plan != "" {
				fmt.Printf("套餐: %s\n", record.Snapshot.Plan)
			}
			return nil
		}
		return withSwitchLoginHint(err, name)
	}
	fmt.Printf("已切换到账号 %q\n", record.Name)
	if record.Snapshot.AccountName != "" {
		fmt.Printf("账户名: %s\n", record.Snapshot.AccountName)
	}
	if record.Snapshot.Email != "" {
		fmt.Printf("邮箱: %s\n", record.Snapshot.Email)
	}
	if backupPath != "" {
		fmt.Printf("切换前备份: %s\n", backupPath)
	}
	fmt.Printf("目标 auth.json: %s\n", application.ActiveAuthPath)
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
	fmt.Printf("已删除账号 %q\n", name)
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
	fmt.Printf("已将账号 %q 重命名为 %q\n", oldName, record.Name)
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
		input, err := promptTextInput(application, fmt.Sprintf("请输入账号 %q 的导出 auth.json 路径，直接回车取消: ", name))
		if err != nil {
			return err
		}
		targetPath = input
	}

	record, targetPath, err := exportAuthAccount(application, name, targetPath)
	if err != nil {
		return err
	}
	fmt.Printf("已将账号 %q 导出到 %s\n", record.Name, targetPath)
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
		input, err := promptTextInput(application, fmt.Sprintf("请输入账号 %q 的导出目录，直接回车取消: ", name))
		if err != nil {
			return err
		}
		dir = input
	}

	record, targetHome, err := exportHomeAccount(application, name, dir, *copyConfig)
	if err != nil {
		return err
	}

	fmt.Printf("已导出账号 %q 到 %s\n", record.Name, targetHome)
	fmt.Printf("可用以下方式启动隔离实例:\n")
	fmt.Printf("  %s\n", shellEnvSetCommand(runtime.GOOS, "CODEX_HOME", targetHome))
	fmt.Printf("  codex\n")
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

	fmt.Printf("已使用隔离 home 启动 Codex: %s\n", targetHome)
	return nil
}

func displayOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
	if _, writeErr := fmt.Fprintf(writer, "账号 %q 还未保存，是否现在登录并保存？ [y/N]: ", name); writeErr != nil {
		return nil, false, nil
	}

	reader := inputReader(application)
	line, readErr := reader.ReadString('\n')
	if readErr != nil && readErr != io.EOF {
		return nil, false, nil
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

	if _, err := fmt.Fprintln(writer, header); err != nil {
		return "", err
	}
	for index, record := range records {
		marker := " "
		if current.Managed != nil && strings.EqualFold(current.Managed.Name, record.Name) {
			marker = "*"
		}
		alias := ""
		if shellName := store.ShellName(record.Name); shellName != "" && !strings.EqualFold(shellName, record.Name) {
			alias = " [" + shellName + "]"
		}
		if _, err := fmt.Fprintf(writer, "  %d. %s %s%s\n", index+1, marker, buildRecordLabel(record), alias); err != nil {
			return "", err
		}
	}
	if _, err := fmt.Fprint(writer, "输入序号或名称，直接回车取消: "); err != nil {
		return "", err
	}

	reader := inputReader(application)
	line, readErr := reader.ReadString('\n')
	if readErr != nil && readErr != io.EOF {
		return "", readErr
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
	if _, err := fmt.Fprint(writer, prompt); err != nil {
		return false, err
	}

	reader := inputReader(application)
	line, readErr := reader.ReadString('\n')
	if readErr != nil && readErr != io.EOF {
		return false, readErr
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
	if _, err := fmt.Fprint(writer, prompt); err != nil {
		return "", err
	}

	reader := inputReader(application)
	line, readErr := reader.ReadString('\n')
	if readErr != nil && readErr != io.EOF {
		return "", readErr
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
	reader := bufio.NewReader(application.Stdin)
	application.Stdin = reader
	return reader
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
