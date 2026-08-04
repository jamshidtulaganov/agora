package main

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jamshidtulaganov/agora/server/internal/cli"
)

// `agora daemon apps` — declare which project(s) this machine serves locally
// and where (daemon-per-dev QA routing, docs/daemon-per-dev-affinity-design.md
// phase 2). Entries live in the profile's CLI config (dev_apps, keyed by
// project id); the daemon reports them to the server as runtime metadata on
// register, and QA tasks for those projects then execute on THIS machine
// against the declared URL.

var daemonAppsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Declare locally-served apps for daemon-per-dev QA routing",
	Long: "Manage this machine's dev-served apps: which project's app runs locally and at what URL.\n" +
		"When the workspace Labs flag 'QA on developer machines' is on, QA tasks for a declared\n" +
		"project execute on this daemon and test the declared URL. Restart the daemon after changes\n" +
		"(agora daemon restart) so the new set is reported to the server.",
}

var daemonAppsSetCmd = &cobra.Command{
	Use:   "set <project> <url>",
	Short: "Declare that this machine serves <project> at <url> (e.g. http://127.0.0.1:8081)",
	Args:  cobra.ExactArgs(2),
	RunE:  runDaemonAppsSet,
}

var daemonAppsUnsetCmd = &cobra.Command{
	Use:   "unset <project>",
	Short: "Remove this machine's declared app for <project>",
	Args:  cobra.ExactArgs(1),
	RunE:  runDaemonAppsUnset,
}

var daemonAppsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List this machine's declared dev-served apps",
	Args:  cobra.NoArgs,
	RunE:  runDaemonAppsList,
}

func init() {
	for _, c := range []*cobra.Command{daemonAppsSetCmd, daemonAppsUnsetCmd, daemonAppsListCmd} {
		c.Flags().String("profile", "", "daemon profile whose config to edit (default: the default profile)")
		daemonAppsCmd.AddCommand(c)
	}
	daemonCmd.AddCommand(daemonAppsCmd)
}

// devAppURLAllowed enforces the design rule: a dev-served URL must be
// loopback/private — it is only ever dereferenced on this machine (by an
// agent running here), never handed to another machine's browser.
func devAppURLAllowed(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid URL %q — expected http(s)://host[:port]", raw)
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		return nil
	}
	return fmt.Errorf("%q is not a loopback/private address — a dev-served app must be local to this machine (the QA agent runs HERE); use a connected box for shared/public targets", host)
}

type devAppProject struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// resolveDevAppProject matches <project> (a UUID or a title, case-insensitive)
// across every workspace the profile's token can see. Ambiguity is an error
// listing the candidates — never a guess.
func resolveDevAppProject(cmd *cobra.Command, cfg cli.CLIConfig, arg string) (devAppProject, error) {
	if cfg.ServerURL == "" || cfg.Token == "" {
		return devAppProject{}, fmt.Errorf("this profile is not logged in — run `agora login` first")
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()

	var workspaces []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	base := cli.NewAPIClient(cfg.ServerURL, "", cfg.Token)
	if err := base.GetJSON(ctx, "/api/workspaces", &workspaces); err != nil {
		return devAppProject{}, fmt.Errorf("list workspaces: %w", err)
	}

	want := strings.ToLower(strings.TrimSpace(arg))
	var matches []devAppProject
	var matchWS []string
	for _, ws := range workspaces {
		client := cli.NewAPIClient(cfg.ServerURL, ws.ID, cfg.Token)
		var resp struct {
			Projects []devAppProject `json:"projects"`
		}
		if err := client.GetJSON(ctx, "/api/projects", &resp); err != nil {
			continue // a workspace we can't read must not block the others
		}
		for _, p := range resp.Projects {
			if p.ID == want || strings.ToLower(p.Title) == want {
				matches = append(matches, p)
				matchWS = append(matchWS, ws.Slug)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return devAppProject{}, fmt.Errorf("no project matches %q (by id or exact title) in any of your workspaces", arg)
	default:
		var lines []string
		for i, m := range matches {
			lines = append(lines, fmt.Sprintf("  %s  %q  (workspace %s)", m.ID, m.Title, matchWS[i]))
		}
		return devAppProject{}, fmt.Errorf("%q is ambiguous — pass the project id instead:\n%s", arg, strings.Join(lines, "\n"))
	}
}

func runDaemonAppsSet(cmd *cobra.Command, args []string) error {
	profile := flagString(cmd, "profile")
	if err := devAppURLAllowed(args[1]); err != nil {
		return err
	}
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}
	project, err := resolveDevAppProject(cmd, cfg, args[0])
	if err != nil {
		return err
	}
	if cfg.DevApps == nil {
		cfg.DevApps = map[string]cli.DevAppEntry{}
	}
	cfg.DevApps[project.ID] = cli.DevAppEntry{URL: strings.TrimSpace(args[1]), Title: project.Title}
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return err
	}
	fmt.Printf("✔ %s → %s\n", project.Title, args[1])
	fmt.Println("Restart the daemon to report it: agora daemon restart" + profileSuffix(profile))
	return nil
}

func runDaemonAppsUnset(cmd *cobra.Command, args []string) error {
	profile := flagString(cmd, "profile")
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}
	want := strings.ToLower(strings.TrimSpace(args[0]))
	for id, entry := range cfg.DevApps {
		if id == want || strings.ToLower(entry.Title) == want {
			delete(cfg.DevApps, id)
			if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
				return err
			}
			fmt.Printf("✔ removed %s\n", entry.Title)
			fmt.Println("Restart the daemon to report it: agora daemon restart" + profileSuffix(profile))
			return nil
		}
	}
	return fmt.Errorf("no declared app matches %q — see `agora daemon apps list`", args[0])
}

func runDaemonAppsList(cmd *cobra.Command, _ []string) error {
	profile := flagString(cmd, "profile")
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}
	if len(cfg.DevApps) == 0 {
		fmt.Println("No dev-served apps declared. Add one: agora daemon apps set <project> <url>")
		return nil
	}
	ids := make([]string, 0, len(cfg.DevApps))
	for id := range cfg.DevApps {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return cfg.DevApps[ids[i]].Title < cfg.DevApps[ids[j]].Title })
	for _, id := range ids {
		e := cfg.DevApps[id]
		title := e.Title
		if title == "" {
			title = id
		}
		fmt.Printf("%-30s %-28s %s\n", title, e.URL, id)
	}
	return nil
}

func profileSuffix(profile string) string {
	if profile == "" {
		return ""
	}
	return " --profile " + profile
}
