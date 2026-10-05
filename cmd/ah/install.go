package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agenthub/internal/tools"
)

// Installing the blackboard instructions for each supported coding agent. The
// tool list, install paths and session detectors live in internal/tools.

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func loadTools() []tools.Tool {
	list, err := tools.Load()
	if err != nil {
		fatal("%v", err)
	}
	return list
}

// pickTools resolves --tool ids; with none, it returns the given default.
func pickTools(list []tools.Tool, ids []string, def func(tools.Tool) bool) []tools.Tool {
	var out []tools.Tool
	if len(ids) == 0 {
		for _, t := range list {
			if def(t) {
				out = append(out, t)
			}
		}
		return out
	}
	for _, id := range ids {
		t, ok := tools.Find(list, id)
		if !ok {
			names := make([]string, len(list))
			for i, t := range list {
				names[i] = t.ID
			}
			fatal("unknown tool %q (known: %s)", id, strings.Join(names, ", "))
		}
		out = append(out, t)
	}
	return out
}

func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	var ids multiFlag
	fs.Var(&ids, "tool", "tool id to install for (repeatable); default: every global tool found on this machine")
	bin := fs.String("bin", "", "path to the ah binary the instructions should call (default: this binary)")
	server := fs.String("server", serverURL(), "hub URL written into the instructions")
	dir := fs.String("dir", ".", "project directory for project-scoped tools")
	fs.Parse(args)

	ahBin := *bin
	if ahBin == "" {
		exe, err := os.Executable()
		if err != nil {
			fatal("cannot find the ah binary: %v", err)
		}
		ahBin = exe
	}
	projectDir, _ := filepath.Abs(*dir)

	chosen := pickTools(loadTools(), ids, func(t tools.Tool) bool { return t.Scope == "global" && t.Detected() })
	if len(chosen) == 0 {
		fmt.Println("no supported tools found on this machine; name one with --tool (see `ah tools`)")
		return
	}
	written := map[string][]string{} // shared file -> tools that use it
	for _, t := range chosen {
		if t.Install.Shared {
			if names, done := written[t.Target(projectDir)]; done {
				written[t.Target(projectDir)] = append(names, t.Name)
				continue
			}
		}
		path, err := installTool(t, ahBin, *server, projectDir)
		if err != nil {
			fatal("%s: %v", t.Name, err)
		}
		if t.Install.Shared {
			written[path] = []string{t.Name}
			continue
		}
		fmt.Printf("installed instructions for %s: %s\n", t.Name, path)
		if t.Note != "" {
			fmt.Printf("  note: %s\n", t.Note)
		}
		for _, old := range removeLegacy(t, projectDir) {
			fmt.Printf("  removed the older %s block from %s\n", "AgentHub", old)
		}
	}
	for path, names := range written {
		fmt.Printf("installed instructions for %s: %s\n", strings.Join(names, " and "), path)
		fmt.Println("  note: this skill file is shared; agents are told to set AH_TOOL to their own tool id")
	}
	for _, t := range chosen {
		if t.Install.Shared {
			for _, old := range removeLegacy(t, projectDir) {
				fmt.Printf("  removed the older AgentHub block from %s\n", old)
			}
		}
	}
}

// removeLegacy strips the AgentHub block from places earlier versions installed
// to, leaving any other content. It returns the files it changed.
func removeLegacy(t tools.Tool, projectDir string) []string {
	var changed []string
	for _, l := range t.Legacy {
		path := l.LegacyTarget(projectDir)
		data, err := os.ReadFile(path)
		if err != nil || !tools.HasSnippet(string(data)) {
			continue
		}
		rest := tools.RemoveSnippet(string(data))
		if strings.TrimSpace(rest) == "" {
			os.Remove(path)
		} else {
			os.WriteFile(path, []byte(rest), 0644)
		}
		changed = append(changed, path)
	}
	return changed
}

// installTool writes the instructions for t and returns the file it wrote.
func installTool(t tools.Tool, ahBin, server, projectDir string) (string, error) {
	text, err := t.Render(ahBin, server)
	if err != nil {
		return "", err
	}
	path := t.Target(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if t.Install.Type == "snippet" {
		existing, _ := os.ReadFile(path)
		text = tools.ReplaceSnippet(string(existing), text)
	}
	return path, os.WriteFile(path, []byte(text), 0644)
}

func cmdUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	var ids multiFlag
	fs.Var(&ids, "tool", "tool id to remove (repeatable); default: every global tool that has it installed")
	dir := fs.String("dir", ".", "project directory for project-scoped tools")
	fs.Parse(args)
	projectDir, _ := filepath.Abs(*dir)

	all := loadTools()
	chosen := pickTools(all, ids, func(t tools.Tool) bool { return t.Scope == "global" })
	inChosen := map[string]bool{}
	for _, t := range chosen {
		inChosen[t.ID] = true
	}
	removed := 0
	for _, t := range chosen {
		for _, old := range removeLegacy(t, projectDir) {
			removed++
			fmt.Printf("removed the older AgentHub block from %s\n", old)
		}
		// A shared file stays unless every tool that uses it is being removed.
		if t.Install.Shared {
			var others []string
			for _, o := range all {
				if o.ID != t.ID && o.Install.Shared && o.Target(projectDir) == t.Target(projectDir) && !inChosen[o.ID] {
					others = append(others, o.ID)
				}
			}
			if len(others) > 0 {
				fmt.Printf("kept %s: it is shared with %s (uninstall them too to remove it)\n", t.Target(projectDir), strings.Join(others, ", "))
				continue
			}
		}
		path, ok := uninstallTool(t, projectDir)
		if ok {
			removed++
			fmt.Printf("removed instructions for %s: %s\n", t.Name, path)
		}
	}
	if removed == 0 {
		fmt.Println("nothing to remove")
	}
}

func uninstallTool(t tools.Tool, projectDir string) (string, bool) {
	path := t.Target(projectDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return path, false
	}
	if t.Install.Type == "skill" {
		os.Remove(path)
		os.Remove(filepath.Dir(path)) // only succeeds when empty
		return path, true
	}
	if !tools.HasSnippet(string(data)) {
		return path, false
	}
	rest := tools.RemoveSnippet(string(data))
	if strings.TrimSpace(rest) == "" {
		os.Remove(path)
	} else {
		os.WriteFile(path, []byte(rest), 0644)
	}
	return path, true
}

// cmdTools lists the supported tools and their state on this machine.
func cmdTools(args []string) {
	cwd, _ := os.Getwd()
	fmt.Printf("%-10s %-18s %-8s %-9s %s\n", "ID", "NAME", "SCOPE", "STATUS", "INSTALL PATH")
	for _, t := range loadTools() {
		status := "-"
		path := t.Target(cwd)
		if data, err := os.ReadFile(path); err == nil && (t.Install.Type == "skill" || tools.HasSnippet(string(data))) {
			status = "installed"
		} else if t.Detected() {
			status = "found"
		}
		fmt.Printf("%-10s %-18s %-8s %-9s %s\n", t.ID, t.Name, t.Scope, status, path)
	}
	fmt.Println("\nstatus: installed = instructions present, found = tool detected but not installed.")
	fmt.Println("project-scoped tools are checked in the current directory. Add tools in ~/.agenthub/tools.json.")
}

// cmdSnippet prints the rendered instructions, for tools without an installer.
func cmdSnippet(args []string) {
	fs := flag.NewFlagSet("snippet", flag.ExitOnError)
	id := fs.String("tool", "agents", "tool id to render for")
	bin := fs.String("bin", "ah", "ah command the instructions should call")
	server := fs.String("server", serverURL(), "hub URL")
	fs.Parse(args)
	t := pickTools(loadTools(), []string{*id}, nil)[0]
	text, err := t.Render(*bin, *server)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)
}
