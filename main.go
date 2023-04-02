package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/celestiaorg/celestia-node/das"
	"github.com/celestiaorg/celestia-node/header"
	"github.com/celestiaorg/go-header/sync"
	"github.com/charmbracelet/bubbles/list"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/celestiaorg/celestia-node/api/rpc/client"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	padding = 2
)

var (
	pad              = strings.Repeat(" ", padding)
	titleStyle       = lipgloss.NewStyle()
	paginationStyle  = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	panelStyle       = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder())
	activePanelStyle = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("69"))
	docStyle         = lipgloss.NewStyle().Margin(1, 1, 1, 1)
)

type panel int

const (
	daserPanel panel = iota
	headerPanel
	syncerPanel
	peerPanel
	bannedPeerPanel
)

type listItem struct {
	title, desc string
}

func (i listItem) Title() string       { return i.title }
func (i listItem) Description() string { return i.desc }
func (i listItem) FilterValue() string { return i.title }

func main() {
	celestiaClient, err := client.NewClient(
		context.TODO(),
		os.Args[1],
		os.Args[2],
	)

	if err != nil {
		panic(err)
	}

	headerSub, err := celestiaClient.Header.Subscribe(context.Background())
	if err != nil {
		panic(err)
	}
	m := model{
		samplingProgress: progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		syncerProgress:   progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		client:           celestiaClient,
		peerList:         createList("Peers"),
		bannedPeerList:   createList("Banned Peers"),
		headerList:       createList("Incoming Headers"),
		headerSub:        headerSub,
	}

	if _, err := tea.NewProgram(&m).Run(); err != nil {
		fmt.Println("Oh no!", err)
		os.Exit(1)
	}
}

func createList(title string) list.Model {
	l := list.New(make([]list.Item, 0), list.NewDefaultDelegate(), 0, 0)
	l.DisableQuitKeybindings()
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Title = title
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	return l
}

type tickMsg time.Time

type model struct {
	client *client.Client

	currentStats     *das.SamplingStats
	samplingProgress progress.Model

	syncStats      *sync.State
	syncerProgress progress.Model

	headerSub  <-chan *header.ExtendedHeader
	headerList list.Model

	peerList list.Model
	peers    []peer.AddrInfo

	bannedPeerList list.Model
	bannedPeers    []peer.AddrInfo

	activePanel   panel
	width, height int
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(waitForActivity(m.headerSub), tickCmd())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case *header.ExtendedHeader:
		return m, m.handleIncomingHeader(msg)
	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.activePanel = (m.activePanel + 1) % 5
		case "b":
			m.handleBanPeer()
		case "u":
			m.handleUnbanPeer()
		}
	case tea.WindowSizeMsg:
		m.height, m.width = msg.Height, msg.Width
		return m, nil
	case tickMsg:
		updateStats := m.updateStats()
		updatePeers := m.updatePeers()
		return m, tea.Batch(tickCmd(), updateStats, updatePeers)
	// FrameMsg is sent when the samplingProgress bar wants to animate itself
	case progress.FrameMsg:
		progressModel, cmd := m.samplingProgress.Update(msg)
		syncerModel, cmd2 := m.syncerProgress.Update(msg)

		m.samplingProgress = progressModel.(progress.Model)
		m.syncerProgress = syncerModel.(progress.Model)
		return m, tea.Batch(cmd, cmd2)
	}

	// handle navigation keypresses for the list panels
	switch m.activePanel {
	case headerPanel:
		var cmd tea.Cmd
		m.headerList, cmd = m.headerList.Update(msg)
		return m, cmd
	case peerPanel:
		var cmd tea.Cmd
		m.peerList, cmd = m.peerList.Update(msg)
		return m, cmd
	case bannedPeerPanel:
		var cmd tea.Cmd
		m.bannedPeerList, cmd = m.bannedPeerList.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m *model) View() string {
	h, _ := docStyle.GetFrameSize()

	peerW, peerH := m.getPanelDimensions(0.33, 0.33)
	headerW, headerH := m.getPanelDimensions(0.66, 0.33)
	daserW, daserH := m.getPanelDimensions(0.66, 0.66)
	syncerW, syncerH := m.getPanelDimensions(0.33, 0.33)

	m.peerList.SetHeight(peerH)
	m.peerList.SetWidth(peerW)
	m.bannedPeerList.SetHeight(peerH)
	m.bannedPeerList.SetWidth(peerW)
	m.headerList.SetHeight(headerH)
	m.headerList.SetWidth(headerW)

	styles := []lipgloss.Style{panelStyle, panelStyle, panelStyle, panelStyle, panelStyle}
	styles[m.activePanel] = activePanelStyle

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.JoinVertical(
			lipgloss.Left,
			styles[0].Width(daserW).Height(daserH).Render(m.daserPanel(h)),
			styles[1].Width(headerW).Height(headerH).Render(m.headerList.View()),
		),
		lipgloss.JoinVertical(
			lipgloss.Right,
			styles[2].Width(syncerW).Height(syncerH).Render(m.syncerPanel(h)),
			styles[3].Width(peerW).Height(peerH).Render(m.peerList.View()),
			styles[4].Width(peerW).Height(peerH).Render(m.bannedPeerList.View()),
		),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/2, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func waitForActivity(sub <-chan *header.ExtendedHeader) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

func (m *model) handleBanPeer() {
	if m.activePanel == peerPanel {
		peerID, err := peer.Decode(m.peerList.SelectedItem().(listItem).Title())
		if err != nil {
			panic(err)
		}
		err = m.client.P2P.BlockPeer(context.TODO(), peerID)
		if err != nil {
			panic(err)
		}
	}
}

func (m *model) handleUnbanPeer() {
	if m.activePanel == bannedPeerPanel {
		peerID, err := peer.Decode(m.bannedPeerList.SelectedItem().(listItem).Title())
		if err != nil {
			panic(err)
		}
		err = m.client.P2P.UnblockPeer(context.TODO(), peerID)
		if err != nil {
			panic(err)
		}
	}
}

func (m *model) handleIncomingHeader(header *header.ExtendedHeader) tea.Cmd {
	var cmd tea.Cmd
	m.headerList.InsertItem(
		len(m.headerList.Items()),
		listItem{title: strconv.FormatInt(header.Height(), 10), desc: header.Hash().String()},
	)

	m.headerList, cmd = m.headerList.Update(header)
	return tea.Batch(cmd, waitForActivity(m.headerSub))
}

func (m *model) getAddrInfo(peer peer.ID) peer.AddrInfo {
	addrInfo, _ := m.client.P2P.PeerInfo(context.TODO(), peer)
	sort.Slice(addrInfo.Addrs, func(i, j int) bool {
		return addrInfo.Addrs[i].String() < addrInfo.Addrs[j].String()
	})
	return addrInfo
}

func (m *model) updateStats() tea.Cmd {
	stats, err := m.client.DAS.SamplingStats(context.TODO())
	if err != nil {
		return nil
	}
	m.currentStats = &stats

	syncStats, err := m.client.Header.SyncState(context.TODO())
	if err != nil {
		return nil
	}
	m.syncStats = &syncStats

	setSamplingProgress := m.samplingProgress.SetPercent(
		float64(stats.SampledChainHead) / float64(stats.NetworkHead),
	)
	setSyncerProgress := m.syncerProgress.SetPercent(
		float64(m.syncStats.Height-m.syncStats.FromHeight) / float64(m.syncStats.ToHeight-m.syncStats.FromHeight),
	)
	return tea.Batch(setSamplingProgress, setSyncerProgress)
}

func (m *model) updatePeers() tea.Cmd {
	return tea.Batch(
		m.refreshPeerList(&m.bannedPeers, &m.bannedPeerList, m.client.P2P.ListBlockedPeers),
		m.refreshPeerList(&m.peers, &m.peerList, m.client.P2P.Peers),
	)
}

func (m *model) refreshPeerList(peerList *[]peer.AddrInfo, uiList *list.Model, fetchPeers func(context.Context) ([]peer.ID, error)) tea.Cmd {
	peers, err := fetchPeers(context.TODO())
	if err != nil {
		return nil
	}
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].String() < peers[j].String()
	})

	if len(peers) != len(*peerList) {
		peerListItems := make([]list.Item, 0, len(peers))
		for _, peer := range peers {
			addrInfo := m.getAddrInfo(peer)
			desc := "No multiaddr found"
			if len(addrInfo.Addrs) > 0 {
				desc = addrInfo.Addrs[0].String()
			}
			peerListItems = append(peerListItems, listItem{title: addrInfo.String(), desc: desc})
		}
		return uiList.SetItems(peerListItems)
	}
	return nil
}

func (m *model) getPanelDimensions(scaleW, scaleH float64) (w, h int) {
	h, v := docStyle.GetFrameSize()
	return int(float64(m.width)*scaleW) - h, int(float64(m.height)*scaleH) - v
}

func (m *model) syncerPanel(frameHeight int) string {
	var syncerPanel string
	if m.syncStats == nil {
		syncerPanel = "Syncer Stats Loading...\n"
	}

	if m.syncStats != nil {
		fromHeight := strconv.FormatUint(m.syncStats.FromHeight, 10)
		toHeight := strconv.FormatUint(m.syncStats.ToHeight, 10)
		syncerHeight := strconv.FormatUint(m.syncStats.Height, 10)

		m.syncerProgress.Width = (m.width / 3) - frameHeight - 4*padding - 1 - len(fromHeight) - len(toHeight)
		syncerPanel = "Syncer Progress: \n\n" +
			pad + fromHeight + pad + m.syncerProgress.View() + pad + toHeight + pad + "\n\n" +
			"Syncer Height: " + syncerHeight
	}

	return syncerPanel
}

func (m *model) daserPanel(frameHeight int) string {
	var daserPanel string
	if m.currentStats == nil {
		daserPanel = "\n" +
			pad + m.samplingProgress.View()
	}

	if m.currentStats != nil {

		m.samplingProgress.Width = (2 * m.width / 3) - frameHeight - 4*padding - 1 - len(strconv.FormatUint(m.currentStats.NetworkHead, 10))
		daserPanel = "DASer Progress: \n" +
			pad + "0" + pad + m.samplingProgress.View() + pad + strconv.FormatUint(m.currentStats.NetworkHead, 10) + pad + "\n\n" +
			pad + "Sampled Chain Head: " + strconv.FormatUint(m.currentStats.SampledChainHead, 10) + "\n" +
			pad + "Network Head: " + strconv.FormatUint(m.currentStats.NetworkHead, 10) + "\n" +
			pad + "Head of Catchup: " + strconv.FormatUint(m.currentStats.CatchupHead, 10) + "\n\n" +
			m.daserWorkers()
	}

	return daserPanel
}

func (m *model) daserWorkers() string {
	sort.Slice(m.currentStats.Workers, func(i, j int) bool {
		return m.currentStats.Workers[i].From < m.currentStats.Workers[j].From
	})

	workerString := "Workers: \n"
	for _, worker := range m.currentStats.Workers {
		progressBar := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())
		from, to := strconv.FormatUint(worker.From, 10), strconv.FormatUint(worker.To, 10)
		percentageComplete := float64(worker.Curr-worker.From) / float64(worker.To-worker.From)
		workerString += pad + from + pad + progressBar.ViewAs(percentageComplete) + pad + to + "\n"
	}
	workerString += "\n\n"

	return workerString
}
