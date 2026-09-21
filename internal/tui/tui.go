package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mheers/talk-to-your-github-stars/internal/config"
	"github.com/mheers/talk-to-your-github-stars/internal/db"
	"github.com/mheers/talk-to-your-github-stars/internal/embed"
	"github.com/mheers/talk-to-your-github-stars/internal/judge"
	"github.com/mheers/talk-to-your-github-stars/internal/llm"
	"github.com/mheers/talk-to-your-github-stars/internal/rag"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#EE6FF8"))
	userStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD7FF"))
	botStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#87FFAF"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F5F"))
	hintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("#333333")).Bold(true)
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EE6FF8")).Bold(true)
)

type tuiMode int

const (
	modeTable tuiMode = iota
	modeSearch
	modeDetail
	modeChat
)

type sendMsg struct{ text string }
type streamMsg struct{ content string }
type doneMsg struct{ err error }

type sortSpec struct {
	label string
	less  func(a, b *db.Repo) bool
}

var sortOptions = []sortSpec{
	{"stars desc", func(a, b *db.Repo) bool { return a.Stars > b.Stars }},
	{"stars asc", func(a, b *db.Repo) bool { return a.Stars < b.Stars }},
	{"name asc", func(a, b *db.Repo) bool { return strings.ToLower(a.FullName) < strings.ToLower(b.FullName) }},
	{"name desc", func(a, b *db.Repo) bool { return strings.ToLower(a.FullName) > strings.ToLower(b.FullName) }},
	{"updated desc", func(a, b *db.Repo) bool { return a.UpdatedAt.After(b.UpdatedAt) }},
	{"updated asc", func(a, b *db.Repo) bool { return a.UpdatedAt.Before(b.UpdatedAt) }},
	{"language asc", func(a, b *db.Repo) bool { return strings.ToLower(a.Language) < strings.ToLower(b.Language) }},
}

// Model is the Bubble Tea model for the TUI.
type Model struct {
	cfg      *config.Config
	db       *db.DB
	embedder *embed.Client
	llm      *llm.Client
	reranker judge.Reranker // optional; nil keeps plain vector retrieval
	program  *tea.Program

	viewport    viewport.Model
	textarea    textarea.Model
	spinner     spinner.Model
	table       table.Model
	searchInput textinput.Model

	messages []string
	waiting  bool
	width    int
	height   int

	mode          tuiMode
	filteredRepos []*db.Repo
	sortIndex     int
	detailRepo    *db.Repo
}

// New creates a new TUI model. reranker may be nil to disable TypeSafe
// judging of retrieval results.
func New(cfg *config.Config, database *db.DB, embedder *embed.Client, llm *llm.Client, reranker judge.Reranker) *Model {
	ta := textarea.New()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.Placeholder = "Ask about your GitHub stars..."

	vp := viewport.New(80, 20)
	vp.SetContent("")

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#EE6FF8"))

	si := textinput.New()
	si.Placeholder = "Search repositories..."
	si.Width = 80

	columns := []table.Column{
		{Title: "Repository", Width: 32},
		{Title: "Description", Width: 48},
		{Title: "Language", Width: 12},
		{Title: "Stars", Width: 8},
		{Title: "Updated", Width: 12},
	}

	tbl := table.New(
		table.WithColumns(columns),
		table.WithRows(nil),
		table.WithFocused(true),
		table.WithHeight(20),
	)

	styles := table.DefaultStyles()
	styles.Header = styles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	styles.Selected = styles.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	tbl.SetStyles(styles)

	m := &Model{
		cfg:         cfg,
		db:          database,
		embedder:    embedder,
		llm:         llm,
		reranker:    reranker,
		textarea:    ta,
		viewport:    vp,
		spinner:     s,
		table:       tbl,
		searchInput: si,
		messages:    []string{titleStyle.Render("Talk to your GitHub stars") + "\nAsk a question and press Enter. Press Esc to return to the table. Ctrl+C to quit."},
		mode:        modeTable,
		sortIndex:   0,
	}

	m.loadRepos()
	return m
}

// SetProgram must be called before Run so the model can send its own messages.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick, textinput.Blink)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textarea.SetWidth(msg.Width)
		m.searchInput.Width = msg.Width - 4
		m.viewport.Width = msg.Width
		m.updateViewportHeight()
		m.table.SetWidth(msg.Width)
		if m.mode == modeTable || m.mode == modeSearch {
			m.table.SetHeight(msg.Height - 7)
		}
		m.refreshViewport()
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeSearch:
			return m.updateSearch(msg)
		case modeDetail:
			return m.updateDetail(msg)
		case modeChat:
			return m.updateChat(msg)
		default:
			return m.updateTable(msg)
		}

	case sendMsg:
		go m.run(msg.text)
		return m, nil

	case streamMsg:
		m.appendBot(msg.content)
		m.refreshViewport()
		return m, nil

	case doneMsg:
		m.waiting = false
		if msg.err != nil {
			m.messages = append(m.messages, errStyle.Render("Error: "+msg.err.Error()))
			m.refreshViewport()
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *Model) updateTable(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "/":
		return m, m.enterSearchMode()
	case "s":
		m.nextSort()
		return m, nil
	case "enter":
		return m.openDetail()
	case "c":
		return m, m.enterChatMode()
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeTable
		m.searchInput.Blur()
		return m, nil
	case "enter":
		return m.openDetail()
	}

	switch msg.Type {
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.runSearch()
	return m, cmd
}

func (m *Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "enter":
		m.mode = modeTable
		m.updateViewportHeight()
		m.rebuildTable()
		return m, nil
	}
	return m, nil
}

func (m *Model) updateChat(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "ctrl+c":
		return m, tea.Quit
	case msg.String() == "esc":
		return m, m.returnToTable()
	case msg.Type == tea.KeyEnter:
		if m.waiting {
			return m, nil
		}
		q := strings.TrimSpace(m.textarea.Value())
		if q == "" {
			return m, nil
		}
		m.messages = append(m.messages, userStyle.Render("You: "+q))
		m.textarea.Reset()
		m.waiting = true
		m.refreshViewport()
		return m, func() tea.Msg { go m.run(q); return nil }
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *Model) enterSearchMode() tea.Cmd {
	m.mode = modeSearch
	m.searchInput.SetValue("")
	m.detailRepo = nil
	m.updateViewportHeight()
	m.runSearch()
	return m.searchInput.Focus()
}

func (m *Model) enterChatMode() tea.Cmd {
	m.mode = modeChat
	m.textarea.Reset()
	m.updateViewportHeight()
	m.refreshViewport()
	return m.textarea.Focus()
}

func (m *Model) returnToTable() tea.Cmd {
	m.mode = modeTable
	m.textarea.Blur()
	m.updateViewportHeight()
	m.rebuildTable()
	return nil
}

func (m *Model) openDetail() (tea.Model, tea.Cmd) {
	if len(m.filteredRepos) == 0 {
		return m, nil
	}
	m.detailRepo = m.filteredRepos[m.table.Cursor()]
	m.mode = modeDetail
	m.updateViewportHeight()
	m.refreshDetailViewport()
	return m, nil
}

func (m *Model) updateViewportHeight() {
	inputHeight := 3
	switch m.mode {
	case modeSearch:
		inputHeight = 1
	case modeDetail:
		inputHeight = 0
	}
	m.viewport.Height = m.height - inputHeight - 4
	if m.viewport.Height < 6 {
		m.viewport.Height = 6
	}
}

func (m *Model) loadRepos() {
	repos, err := m.db.ListRepos()
	if err != nil {
		m.filteredRepos = nil
		m.rebuildTable()
		return
	}
	m.filteredRepos = repos
	m.applySort()
	m.rebuildTable()
}

func (m *Model) runSearch() {
	query := m.searchInput.Value()
	var results []*db.Repo
	var err error
	if strings.TrimSpace(query) == "" {
		results, err = m.db.ListRepos()
	} else {
		results, err = m.db.SearchRepos(query)
	}
	if err != nil {
		m.filteredRepos = nil
		m.rebuildTable()
		return
	}
	m.filteredRepos = results
	m.applySort()
	m.rebuildTable()
}

func (m *Model) applySort() {
	if m.sortIndex >= len(sortOptions) {
		m.sortIndex = 0
	}
	less := sortOptions[m.sortIndex].less
	sort.Slice(m.filteredRepos, func(i, j int) bool {
		return less(m.filteredRepos[i], m.filteredRepos[j])
	})
}

func (m *Model) nextSort() {
	m.sortIndex = (m.sortIndex + 1) % len(sortOptions)
	m.applySort()
	m.rebuildTable()
}

func (m *Model) rebuildTable() {
	rows := make([]table.Row, len(m.filteredRepos))
	for i, r := range m.filteredRepos {
		updated := ""
		if !r.UpdatedAt.IsZero() {
			updated = r.UpdatedAt.Format("2006-01-02")
		}
		rows[i] = table.Row{
			r.FullName,
			truncate(r.Description, 48),
			r.Language,
			fmt.Sprintf("%d", r.Stars),
			updated,
		}
	}
	m.table.SetRows(rows)
	if m.table.Cursor() >= len(rows) {
		m.table.SetCursor(len(rows) - 1)
	}
	if m.table.Cursor() < 0 && len(rows) > 0 {
		m.table.SetCursor(0)
	}
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	switch m.mode {
	case modeChat:
		return m.chatView()
	case modeDetail:
		return m.detailView()
	default:
		return m.tableView()
	}
}

func (m *Model) tableView() string {
	header := titleStyle.Render("Repository table viewer")
	var search string
	if m.mode == modeSearch {
		search = cursorStyle.Render("> ") + m.searchInput.View()
	} else {
		if m.searchInput.Value() == "" {
			search = hintStyle.Render("press / to search")
		} else {
			search = hintStyle.Render("search: " + m.searchInput.Value())
		}
	}
	sortLabel := sortOptions[m.sortIndex].label
	footer := hintStyle.Render(fmt.Sprintf("Sort: %s • %d repos • / search • s sort • enter detail • c chat • ctrl+c quit", sortLabel, len(m.filteredRepos)))
	return header + "\n" + search + "\n" + m.table.View() + "\n" + footer
}

func (m *Model) chatView() string {
	var footer string
	if m.waiting {
		footer = m.spinner.View() + " thinking..."
	} else {
		footer = "Ready — press Esc to return to table, Ctrl+C to quit"
	}
	return m.viewport.View() + "\n" + m.textarea.View() + "\n" + hintStyle.Render(footer)
}

func (m *Model) detailView() string {
	footer := hintStyle.Render("Esc or Enter: back to table")
	return titleStyle.Render("Repository details") + "\n\n" + m.viewport.View() + "\n" + footer
}

func (m *Model) refreshViewport() {
	switch m.mode {
	case modeDetail:
		m.refreshDetailViewport()
	default:
		m.viewport.SetContent(strings.Join(m.messages, "\n\n"))
		m.viewport.GotoBottom()
	}
}

func (m *Model) refreshDetailViewport() {
	if m.detailRepo == nil {
		m.viewport.SetContent("")
		return
	}
	r := m.detailRepo
	var b strings.Builder
	b.WriteString(titleStyle.Render(r.FullName) + "\n\n")
	if r.Description != "" {
		b.WriteString(r.Description + "\n\n")
	}
	b.WriteString(fmt.Sprintf("⭐ %d  Language: %s  Forks: %d  Open issues: %d\n", r.Stars, r.Language, r.Forks, r.OpenIssues))
	if len(r.Topics) > 0 {
		b.WriteString(fmt.Sprintf("Topics: %s\n", strings.Join(r.Topics, ", ")))
	}
	if r.Homepage != "" {
		b.WriteString(fmt.Sprintf("Homepage: %s\n", r.Homepage))
	}
	if r.URL != "" {
		b.WriteString(fmt.Sprintf("URL: %s\n", r.URL))
	}
	if r.ReadmeText != "" {
		b.WriteString("\n" + truncate(r.ReadmeText, 2000) + "\n")
	}
	m.viewport.SetContent(b.String())
}

func (m *Model) appendBot(text string) {
	const prefix = "Assistant: "
	if len(m.messages) == 0 || !strings.HasPrefix(m.messages[len(m.messages)-1], botStyle.Render(prefix)) {
		m.messages = append(m.messages, botStyle.Render(prefix)+text)
		return
	}
	m.messages[len(m.messages)-1] += text
}

func (m *Model) run(query string) {
	ctx := context.Background()

	retrieval, err := rag.Retrieve(ctx, m.db, m.embedder, m.reranker, query, 10)
	if err != nil {
		m.program.Send(doneMsg{err: err})
		return
	}
	if retrieval.RerankErr != nil {
		m.program.Send(streamMsg{content: hintStyle.Render("(re-ranking unavailable; showing raw search results)") + "\n"})
	}
	if len(retrieval.Results) == 0 {
		m.program.Send(streamMsg{content: rag.NoMatchMessage})
		m.program.Send(doneMsg{})
		return
	}
	results := retrieval.Results

	var sources []string
	seen := map[string]bool{}
	for _, r := range results {
		if !seen[r.RepoFullName] {
			sources = append(sources, r.RepoFullName)
			seen[r.RepoFullName] = true
		}
	}
	if len(sources) > 0 {
		m.program.Send(streamMsg{content: fmt.Sprintf("\nSources: %s\n\n", strings.Join(sources, ", "))})
	}

	system := rag.BuildSystemPrompt(results)
	out := make(chan string)
	go func() {
		defer close(out)
		if err := m.llm.Ask(ctx, system, query, out); err != nil {
			m.program.Send(doneMsg{err: err})
			return
		}
		m.program.Send(doneMsg{})
	}()

	for chunk := range out {
		m.program.Send(streamMsg{content: chunk})
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
