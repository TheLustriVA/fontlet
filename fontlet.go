package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Configuration ---
const (
	previewLines = 11 // Number of lines for in-list previews
	// previewWidth will be dynamically set based on terminal width for figlet rendering
)

// --- Styles ---
var (
	docStyle             = lipgloss.NewStyle().Margin(1, 2)
	titleStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("62")).Bold(true).MarginBottom(1)
	subtitleStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("242")).MarginBottom(1)
	helpStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).MarginTop(1)
	errorStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	successStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("76")).Bold(true) // Green for success
	figletOutputStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("69"))             // Purple for figlet output
	listTitleStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true).Padding(0, 0, 0, 0).MarginBottom(1)
	inputPromptStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Bold(true)
	inputValueStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	statusMessageStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Padding(1, 0) // Orange for status/choices

	// For custom list item delegate
	itemStyle         = lipgloss.NewStyle().PaddingLeft(2)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(0).Foreground(lipgloss.Color("208")) // Orange for selected item
	previewTextStyle  = lipgloss.NewStyle().Faint(true)                 // Faint for preview text
	fontNameStyle     = lipgloss.NewStyle().Bold(true)
)

// --- Application States ---
type appState int

const (
	stateInitialLoading appState = iota // Checking figlet, initial font scan
	stateInputText
	stateLoadingPreviews // After text input, generating previews for all fonts
	stateSelectFontWithPreview
	stateGeneratingFullOutput // After font selection, generating the full output
	stateOutputChoice         // (t)erminal or (f)ile?
	stateSaveFileNameInput
	stateDisplayFiglet
	stateShowStatusMessage // For brief messages like "Saved!"
	stateError
)

// --- Model ---
type model struct {
	state            appState
	textInput        textinput.Model // For user's main text and filename input
	fontList         list.Model
	figletViewport   viewport.Model
	spinner          spinner.Model
	fonts            []fontMetadata // Holds path, name, and pre-rendered preview
	fullFigletOutput string
	inputText        string
	selectedFontMeta fontMetadata // Store the chosen font's metadata
	termWidth        int
	termHeight       int
	errorMessage     string
	statusMessage    string   // For temporary messages like "Saved!" or choices
	figletCmdPath    string
}

type fontMetadata struct {
	Name          string // e.g., "standard"
	Path          string // e.g., "/usr/share/figlet/standard.flf"
	PreviewRender string // Truncated figlet output for list display
}

// For list.Item interface
func (fm fontMetadata) Title() string       { return fm.Name } // Used for filtering
func (fm fontMetadata) Description() string { return fm.PreviewRender } // Not directly used by default delegate
func (fm fontMetadata) FilterValue() string { return fm.Name }


// --- Messages ---
type initialResourcesLoadedMsg struct{ fonts []fontMetadata } // Fonts without previews initially
type previewsGeneratedMsg struct{ fontsWithPreviews []fontMetadata }
type fullFigletRenderedMsg struct{ output string }
type fileSavedMsg struct { path string }
type errorMsg struct{ err error }
type statusTimeoutMsg struct{} // To clear status messages


func initialModel() model {
	// Figlet check
	cmdPath, err := exec.LookPath("figlet")
	if err != nil {
		return model{
			state:        stateError,
			errorMessage: "figlet command not found. Please install figlet to use this script.",
		}
	}

	ti := textinput.New()
	ti.Placeholder = "Enter text to figletize..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.PromptStyle = inputPromptStyle
	ti.TextStyle = inputValueStyle

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))


	return model{
		state:         stateInitialLoading,
		textInput:     ti,
		spinner:       s,
		figletCmdPath: cmdPath,
	}
}

func (m model) Init() tea.Cmd {
	if m.state == stateError {
		return tea.Quit // Quit immediately if figlet not found
	}
	return tea.Batch(m.spinner.Tick, m.loadInitialFontsCmd())
}

// --- Commands ---
func (m model) loadInitialFontsCmd() tea.Cmd {
	return func() tea.Msg {
		fonts, err := findFigletFonts() // This just gets names and paths
		if err != nil {
			return errorMsg{err}
		}
		return initialResourcesLoadedMsg{fonts}
	}
}

func (m model) generatePreviewsCmd() tea.Cmd {
	return func() tea.Msg {
		fontsWithPreviews := make([]fontMetadata, len(m.fonts))
		// Determine a reasonable preview width, slightly less than terminal width
		// Figlet's -w is in characters, not pixels.
		// Subtracting some for list padding and scrollbar.
		previewRenderWidth := m.termWidth - 20 
		if previewRenderWidth < 20 {
			previewRenderWidth = 20 // Minimum sensible width
		}

		for i, font := range m.fonts {
			output, err := runFiglet(m.figletCmdPath, font.Path, m.inputText, previewRenderWidth)
			if err != nil {
				// Store error or a placeholder in preview
				font.PreviewRender = fmt.Sprintf("Error rendering: %v", err)
			} else {
				font.PreviewRender = truncateString(output, previewLines)
			}
			fontsWithPreviews[i] = font
		}
		return previewsGeneratedMsg{fontsWithPreviews}
	}
}

func (m model) renderFullFigletCmd(fontPath, text string) tea.Cmd {
	return func() tea.Msg {
		// For full output, use a generous width or terminal width
		// Subtract a bit for docStyle margins
		renderWidth := m.termWidth - docStyle.GetHorizontalFrameSize() - 4 
		if renderWidth < 20 { renderWidth = 20 }

		output, err := runFiglet(m.figletCmdPath, fontPath, text, renderWidth)
		if err != nil {
			return errorMsg{fmt.Errorf("failed to run figlet for full output: %w", err)}
		}
		return fullFigletRenderedMsg{output}
	}
}

func (m model) saveToFileCmd(filename, content string) tea.Cmd {
    return func() tea.Msg {
        err := os.WriteFile(filename, []byte(content), 0644)
        if err != nil {
            return errorMsg{fmt.Errorf("failed to save file '%s': %w", filename, err)}
        }
        return fileSavedMsg{path: filename}
    }
}


// --- Helper Functions ---
func findFigletFonts() ([]fontMetadata, error) {
	// (This function is largely the same as before, just ensuring it returns fontMetadata without previews yet)
	var fontDir string
	var fontPaths []string

	cmd := exec.Command("figlet", "-I", "2")
	output, err := cmd.Output()
	if err == nil {
		fontDir = strings.TrimSpace(string(output))
		potentialFontDir := filepath.Join(fontDir, "fonts")
		if fi, err := os.Stat(potentialFontDir); err == nil && fi.IsDir() {
			fontDir = potentialFontDir
		} else if fi, err := os.Stat(fontDir); !(err == nil && fi.IsDir()) {
			fontDir = "" // Reset if not a valid dir
		}
	}

	if fontDir == "" {
		commonDirs := []string{"/usr/share/figlet/fonts", "/usr/share/figlet", "/usr/local/share/figlet/fonts", "/usr/local/share/figlet", "/opt/homebrew/share/figlet/fonts", "/opt/homebrew/share/figlet"}
		for _, dir := range commonDirs {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				fontDir = dir
				break
			}
		}
	}
	if fontDir == "" { return nil, fmt.Errorf("could not find figlet font directory") }

	err = filepath.WalkDir(fontDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".flf") {
			fontPaths = append(fontPaths, path)
		}
		return nil
	})
	if err != nil { return nil, fmt.Errorf("error walking font directory %s: %w", fontDir, err) }
	if len(fontPaths) == 0 { return nil, fmt.Errorf("no .flf font files found in %s or subdirectories", fontDir) }

	var fonts []fontMetadata
	for _, p := range fontPaths {
		nameWithExt := filepath.Base(p)
		name := strings.TrimSuffix(nameWithExt, filepath.Ext(nameWithExt))
		fonts = append(fonts, fontMetadata{Name: name, Path: p}) // PreviewRender is empty initially
	}
	sort.Slice(fonts, func(i, j int) bool { return fonts[i].Name < fonts[j].Name })
	return fonts, nil
}

func runFiglet(figletCmdPath, fontPath, text string, width int) (string, error) {
	cmd := exec.Command(figletCmdPath, "-f", fontPath, "-w", fmt.Sprintf("%d", width), text)
	output, err := cmd.Output()
	if err != nil {
		// Try without -w if it failed (some figlet versions/fonts might not like it or small widths)
		cmd = exec.Command(figletCmdPath, "-f", fontPath, text)
		output, err = cmd.Output()
		if err != nil {
		    return "", fmt.Errorf("figlet failed (path: %s, text: %s, width: %d): %w", fontPath, text, width, err)
		}
	}
	return string(output), nil
}

func truncateString(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	// Trim trailing empty lines that might result from figlet output
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}


// --- Custom List Item Delegate ---
type itemDelegate struct {
	Styles           *delegateStyles
	PreviewLines int // Max lines for preview
}

type delegateStyles struct {
	NormalTitle   lipgloss.Style
	SelectedTitle lipgloss.Style
	NormalPreview lipgloss.Style
	SelectedPreview lipgloss.Style
	FontName lipgloss.Style
}

func newItemDelegate() *itemDelegate {
	// Define styles for the delegate here
	// These will be used in the Render method
	return &itemDelegate{
		Styles: &delegateStyles{
			NormalTitle:   itemStyle.Copy().Height(1), // Base style for the item line
			SelectedTitle: selectedItemStyle.Copy().Height(1),
			NormalPreview: itemStyle.Copy().Faint(true),
			SelectedPreview: selectedItemStyle.Copy().Foreground(lipgloss.Color("208")).Faint(false), // Selected preview less faint
			FontName: fontNameStyle,
		},
		PreviewLines: previewLines,
	}
}

func (d *itemDelegate) Height() int {
	// Height for font name + preview lines + 1 for spacing or ensure enough space
	return 1 + d.PreviewLines + 1
}

func (d *itemDelegate) Spacing() int { return 1 }

func (d *itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d *itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(fontMetadata)
	if !ok {
		return
	}

	var styledName, styledPreview string
	isSelected := index == m.Index()

	nameStr := d.Styles.FontName.Render(item.Name)

	if isSelected {
		styledName = d.Styles.SelectedTitle.Render("➤ " + nameStr)
		styledPreview = d.Styles.SelectedPreview.Render(item.PreviewRender)
	} else {
		styledName = d.Styles.NormalTitle.Render("  " + nameStr)
		styledPreview = d.Styles.NormalPreview.Render(item.PreviewRender)
	}
	
	// Ensure preview doesn't overflow delegate height by truncating it again (should be pre-truncated)
	// This is more about how it's laid out here.
	previewLinesRender := strings.Split(styledPreview, "\n")
	if len(previewLinesRender) > d.PreviewLines {
		previewLinesRender = previewLinesRender[:d.PreviewLines]
	}


	fmt.Fprintf(w, "%s\n%s", styledName, strings.Join(previewLinesRender, "\n"))
}


// --- Update ---
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		h, _ := docStyle.GetFrameSize()
		m.textInput.Width = msg.Width - h - lipgloss.Width(m.textInput.Prompt) -1
		
		// Recalculate list and viewport sizes
		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.footerView())
		listHeight := m.termHeight - headerHeight - footerHeight - 2 // Adjusted for margins

		if m.fontList.Items() != nil { // Check if list is initialized
    		m.fontList.SetSize(msg.Width-h, listHeight)
		}
		m.figletViewport.Width = msg.Width - h
		m.figletViewport.Height = listHeight


	case spinner.TickMsg:
		if m.state == stateInitialLoading || m.state == stateLoadingPreviews || m.state == stateGeneratingFullOutput {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	
	case initialResourcesLoadedMsg:
		m.fonts = msg.fonts // Fonts without previews yet
		m.state = stateInputText
		m.textInput.Focus() // Focus input after initial load

	case previewsGeneratedMsg:
		m.fonts = msg.fontsWithPreviews // Now fonts have previews
		items := make([]list.Item, len(m.fonts))
		for i, f := range m.fonts {
			items[i] = f
		}
		
		delegate := newItemDelegate()
		listHeight := m.termHeight - lipgloss.Height(m.headerView()) - lipgloss.Height(m.footerView()) -2
		newList := list.New(items, delegate, m.termWidth-docStyle.GetHorizontalFrameSize(), listHeight)
		newList.Title = "Available Fonts (with Previews)"
		newList.Styles.Title = listTitleStyle
		newList.Styles.HelpStyle = helpStyle.Copy().MarginTop(0) // Adjust help style margin for list
		newList.SetShowStatusBar(true) // Show item count, etc.
		newList.SetFilteringEnabled(true)
		newList.Styles.StatusBar = statusMessageStyle.Copy().Padding(0,1)


		m.fontList = newList
		m.state = stateSelectFontWithPreview
	
	case fullFigletRenderedMsg:
		m.fullFigletOutput = msg.output
		m.state = stateOutputChoice
		m.statusMessage = "Output to (t)erminal or save to (f)ile?"


	case fileSavedMsg:
		m.statusMessage = successStyle.Render(fmt.Sprintf("Saved to %s!", msg.path))
		m.state = stateShowStatusMessage
		// Return to font selection after a brief moment
		cmds = append(cmds, tea.Tick(time.Second*2, func(t time.Time) tea.Msg { return statusTimeoutMsg{} }))
	
	case statusTimeoutMsg:
		m.statusMessage = ""
		m.state = stateSelectFontWithPreview // Or stateInputText if preferred

	case errorMsg:
		m.errorMessage = msg.err.Error()
		m.state = stateError
		return m, nil // Stop further processing on error

	case tea.KeyMsg:
		// Global quit
		if key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit"))) {
			return m, tea.Quit
		}

		switch m.state {
		case stateInputText:
			if msg.Type == tea.KeyEnter {
				m.inputText = strings.TrimSpace(m.textInput.Value())
				if m.inputText != "" {
					m.state = stateLoadingPreviews
					m.textInput.Blur()
					cmds = append(cmds, m.spinner.Tick, m.generatePreviewsCmd())
				}
			} else {
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case stateSelectFontWithPreview:
			if key.Matches(msg, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))) {
				m.state = stateInputText
				m.textInput.SetValue(m.inputText) // Keep previous text
				m.textInput.Focus()
				return m, nil
			}
			if msg.Type == tea.KeyEnter {
				selected, ok := m.fontList.SelectedItem().(fontMetadata)
				if ok {
					m.selectedFontMeta = selected
					m.state = stateGeneratingFullOutput
					cmds = append(cmds, m.spinner.Tick, m.renderFullFigletCmd(m.selectedFontMeta.Path, m.inputText))
				}
			}
			var cmd tea.Cmd
			m.fontList, cmd = m.fontList.Update(msg)
			cmds = append(cmds, cmd)
		
		case stateOutputChoice:
			switch strings.ToLower(msg.String()) {
			case "t":
				m.figletViewport = viewport.New(m.termWidth-docStyle.GetHorizontalFrameSize(), m.termHeight - lipgloss.Height(m.headerView()) - lipgloss.Height(m.footerView()) -2)
				m.figletViewport.Style = figletOutputStyle
				m.figletViewport.SetContent(m.fullFigletOutput)
				m.figletViewport.GotoTop()
				m.state = stateDisplayFiglet
				m.statusMessage = ""
			case "f":
				m.textInput.Placeholder = "Enter filename (e.g., output.txt)"
				m.textInput.SetValue("") // Clear for filename
				m.textInput.Focus()
				m.state = stateSaveFileNameInput
				m.statusMessage = ""
			case "esc": // Allow escape from this choice
				m.state = stateSelectFontWithPreview
				m.statusMessage = ""
			}

		case stateSaveFileNameInput:
			if msg.Type == tea.KeyEnter {
				filename := strings.TrimSpace(m.textInput.Value())
				if filename != "" {
					m.textInput.Blur()
					cmds = append(cmds, m.saveToFileCmd(filename, m.fullFigletOutput))
				}
			} else if msg.Type == tea.KeyEsc {
				m.state = stateOutputChoice // Go back to T/F choice
				m.statusMessage = "Output to (t)erminal or save to (f)ile?"
				m.textInput.Blur()
			} else {
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				cmds = append(cmds, cmd)
			}

		case stateDisplayFiglet:
			if key.Matches(msg, key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "back"))) {
				m.state = stateSelectFontWithPreview
			}
			var cmd tea.Cmd
			m.figletViewport, cmd = m.figletViewport.Update(msg)
			cmds = append(cmds, cmd)
		
		case stateShowStatusMessage: // Usually waiting for timeout or a key press
		    if key.Matches(msg, key.NewBinding(key.WithKeys("enter", "esc"), key.WithHelp("any key", "continue"))) {
				m.statusMessage = ""
				m.state = stateSelectFontWithPreview
			}

		case stateError: // Allow quitting from error state
			if key.Matches(msg, key.NewBinding(key.WithKeys("q", "esc", "enter"), key.WithHelp("any key", "quit"))) {
				return m, tea.Quit
			}
		}
	}
	return m, tea.Batch(cmds...)
}

// --- View ---
func (m model) headerView() string {
	title := titleStyle.Render("FontLet GO v2 🎨")
	var subtitle string
	// (Subtitles can be added based on state if desired)
	return fmt.Sprintf("%s\n%s", title, subtitle)
}

func (m model) footerView() string {
	var help string
	switch m.state {
	case stateInputText:
		help = helpStyle.Render("enter: confirm text • ctrl+c: quit")
	case stateSelectFontWithPreview:
		// List provides its own help usually, or we can add more context.
		// help = m.fontList.View() // This would render the list itself. We want just help.
		help = helpStyle.Render("↑/↓: navigate • enter: select font • esc: change text • ctrl+c: quit")
	case stateDisplayFiglet:
		help = helpStyle.Render("↑/↓/pgup/pgdn: scroll • esc/q: back to font list • ctrl+c: quit")
	case stateOutputChoice:
		help = helpStyle.Render("t: terminal • f: file • esc: back to font list • ctrl+c: quit")
	case stateSaveFileNameInput:
		help = helpStyle.Render("enter: save file • esc: cancel save • ctrl+c: quit")
	case stateInitialLoading, stateLoadingPreviews, stateGeneratingFullOutput:
		return fmt.Sprintf("%s %s", m.spinner.View(), "Processing...")
	case stateError:
		help = helpStyle.Render("Press any key to quit.")
	case stateShowStatusMessage:
		help = helpStyle.Render("Press Enter or Esc to continue...")
	}
	return help
}


func (m model) View() string {
	if m.termWidth == 0 { return "Initializing..." } // Avoid rendering before size is known

	var s strings.Builder
	s.WriteString(m.headerView())
	s.WriteString("\n") // Some space after header

	mainContentStyle := lipgloss.NewStyle().Width(m.termWidth - docStyle.GetHorizontalFrameSize())

	switch m.state {
	case stateError:
		s.WriteString(mainContentStyle.Render(errorStyle.Render(m.errorMessage)))
	case stateInitialLoading, stateLoadingPreviews, stateGeneratingFullOutput:
		s.WriteString(mainContentStyle.Render(fmt.Sprintf("\n%s Please wait...\n", m.spinner.View())))
	case stateInputText:
		s.WriteString(m.textInput.View())
	case stateSelectFontWithPreview:
		s.WriteString(m.fontList.View()) // List handles its own height/width
	case stateDisplayFiglet:
		s.WriteString(m.figletViewport.View())
	case stateOutputChoice:
		s.WriteString(mainContentStyle.Render(statusMessageStyle.Render(m.statusMessage)))
	case stateSaveFileNameInput:
		s.WriteString(m.textInput.View()) // Re-using textInput for filename
	case stateShowStatusMessage:
	    s.WriteString(mainContentStyle.Render(m.statusMessage)) // Already styled success/error
	}

	s.WriteString("\n\n") // Space before footer
	s.WriteString(m.footerView())

	return docStyle.Render(s.String())
}


func main() {
	m := initialModel()
	if m.state == stateError && m.errorMessage != "" { // Check if error occurred in initialModel
		fmt.Fprintln(os.Stderr, errorStyle.Render(m.errorMessage))
		os.Exit(1)
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}

