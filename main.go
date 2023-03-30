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
	padding  = 2
	maxWidth = 80
)

var (
	titleStyle        = lipgloss.NewStyle()
	itemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
	paginationStyle   = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	quitTextStyle     = lipgloss.NewStyle().Margin(1, 0, 2, 4)
	panelStyle        = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder())
	activePanelStyle  = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("69"))
)

type panelDimensions struct {
	w, h int
}

type item struct {
	title, desc string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

var docStyle = lipgloss.NewStyle().Margin(1, 1, 1, 1)

var helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render

func waitForActivity(sub <-chan *header.ExtendedHeader) tea.Cmd {
	return func() tea.Msg {
		header := <-sub
		return header
	}
}

func main() {
	celestiaClient, err := client.NewClient(context.TODO(), "ws://localhost:26658", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJBbGxvdyI6WyJwdWJsaWMiLCJyZWFkIiwid3JpdGUiLCJhZG1pbiJdfQ.AShMh2DTTd7DsoGfZVUIPPhH5h8ezPxOyS_LdjTI2G8")

	if err != nil {
		panic(err)
	}

	peerList := createList("Peers")
	bannedPeerList := createList("Banned Peers")
	headerList := createList("Incoming Headers")

	headerSub, err := celestiaClient.Header.Subscribe(context.Background())
	if err != nil {
		panic(err)
	}
	m := model{
		samplingProgress: progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		client:           celestiaClient,
		width:            0,
		height:           0,
		peerList:         peerList,
		bannedPeerList:   bannedPeerList,
		headerList:       headerList,
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
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle

	return l
}

type tickMsg time.Time

type model struct {
	samplingProgress progress.Model
	client           *client.Client
	currentStats     *das.SamplingStats
	syncStats        *sync.State
	headerSub        <-chan *header.ExtendedHeader
	headerList       list.Model
	peerList         list.Model
	bannedPeerList   list.Model
	bannedPeers      []peer.AddrInfo
	peers            []peer.AddrInfo
	selectedPeer     string
	activePanel      int
	width            int
	height           int
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(waitForActivity(m.headerSub), tickCmd())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case *header.ExtendedHeader:
		m.headerList.InsertItem(len(m.headerList.Items()), item{title: strconv.FormatInt(msg.Height(), 10), desc: msg.Hash().String()})
		var cmd tea.Cmd
		m.headerList, cmd = m.headerList.Update(msg)
		return m, tea.Batch(cmd, waitForActivity(m.headerSub))
	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.activePanel = (m.activePanel + 1) % 5
		case "b":
			if m.activePanel == 3 {
				peerID, err := peer.Decode(m.peerList.SelectedItem().(item).Title())
				if err != nil {
					panic(err)
				}
				err = m.client.P2P.BlockPeer(context.TODO(), peerID)
				if err != nil {
					panic(err)
				}
			}
		case "u":
			if m.activePanel == 4 {
				peerID, err := peer.Decode(m.bannedPeerList.SelectedItem().(item).Title())
				if err != nil {
					panic(err)
				}
				err = m.client.P2P.UnblockPeer(context.TODO(), peerID)
				if err != nil {
					panic(err)
				}
			}
		}

	case tea.WindowSizeMsg:
		m.height, m.width = msg.Height, msg.Width
		return m, nil

	case tickMsg:
		stats, err := m.client.DAS.SamplingStats(context.TODO())
		m.currentStats = &stats
		if err != nil {
			panic(err)
		}

		syncStats, err := m.client.Header.SyncState(context.TODO())
		m.syncStats = &syncStats
		if err != nil {
			panic(err)
		}

		m.updatePeers()

		// Note that you can also use samplingProgress.Model.SetPercent to set the
		// percentage value explicitly, too.
		cmd := m.samplingProgress.SetPercent(float64(stats.SampledChainHead) / float64(stats.NetworkHead))
		return m, tea.Batch(tickCmd(), cmd)

	// FrameMsg is sent when the samplingProgress bar wants to animate itself
	case progress.FrameMsg:
		progressModel, cmd := m.samplingProgress.Update(msg)
		m.samplingProgress = progressModel.(progress.Model)
		return m, cmd
	}

	if m.activePanel == 1 {
		var cmd tea.Cmd
		m.headerList, cmd = m.headerList.Update(msg)
		return m, cmd
	} else if m.activePanel == 3 {
		var cmd tea.Cmd
		m.peerList, cmd = m.peerList.Update(msg)
		return m, cmd
	} else if m.activePanel == 4 {
		var cmd tea.Cmd
		m.bannedPeerList, cmd = m.bannedPeerList.Update(msg)
		return m, cmd
	} else {
		return m, nil
	}
}

func (m *model) getAddrInfo(peer peer.ID) peer.AddrInfo {
	addrInfo, _ := m.client.P2P.PeerInfo(context.TODO(), peer)
	sort.Slice(addrInfo.Addrs, func(i, j int) bool {
		return addrInfo.Addrs[i].String() < addrInfo.Addrs[j].String()
	})
	return addrInfo
}

func (m *model) updatePeers() {
	banned := make(map[string]struct{})
	bannedPeers, _ := m.client.P2P.ListBlockedPeers(context.TODO())
	sort.Slice(bannedPeers, func(i, j int) bool {
		return bannedPeers[i].String() < bannedPeers[j].String()
	})
	if len(bannedPeers) != len(m.bannedPeers) {
		m.bannedPeers = make([]peer.AddrInfo, 0)
		for _, peer := range bannedPeers {
			banned[peer.String()] = struct{}{}
			m.bannedPeers = append(m.bannedPeers, m.getAddrInfo(peer))
		}
		var peerListItems []list.Item
		for _, peer := range m.bannedPeers {
			desc := "No multiaddr found"
			if len(peer.Addrs) > 0 {
				desc = peer.Addrs[0].String()
			}
			peerListItems = append(peerListItems, item{title: peer.ID.String(), desc: desc})
		}
		m.bannedPeerList.SetItems(peerListItems)
	}

	peers, _ := m.client.P2P.Peers(context.TODO())
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].String() < peers[j].String()
	})
	if len(peers)-len(bannedPeers) != len(m.peers) {
		m.peers = make([]peer.AddrInfo, 0)
		for _, peer := range peers {
			_, ok := banned[peer.String()]
			if !ok {
				m.peers = append(m.peers, m.getAddrInfo(peer))
			}
		}
		var peerListItems []list.Item
		for _, peer := range m.peers {
			desc := "No multiaddr found"
			if len(peer.Addrs) > 0 {
				desc = peer.Addrs[0].String()
			}
			peerListItems = append(peerListItems, item{title: peer.ID.String(), desc: desc})
		}
		m.peerList.SetItems(peerListItems)
	}
}

func (m *model) getPanelDimensions(scaleW, scaleH float64) (w, h int) {
	h, v := docStyle.GetFrameSize()
	return int(float64(m.width)*scaleW) - h, int(float64(m.height)*scaleH) - v
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

func (m *model) syncerPanel(frameHeight int) string {
	var syncerPanel string
	pad := strings.Repeat(" ", padding)
	if m.syncStats == nil {
		syncerPanel = "Syncer Stats Loading...\n"
	}

	if m.syncStats != nil {
		progressBar := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())
		progressBar.Width = (m.width / 3) - frameHeight - 4*padding - 1 - len(strconv.FormatUint(m.syncStats.FromHeight, 10)) - len(strconv.FormatUint(m.syncStats.ToHeight, 10))
		syncerPanel = "Syncer Progress: \n\n" +
			pad + strconv.FormatUint(m.syncStats.FromHeight, 10) + pad + progressBar.ViewAs(float64(m.syncStats.ToHeight-m.syncStats.FromHeight)/float64(m.syncStats.ToHeight-m.syncStats.FromHeight)) + pad + strconv.FormatUint(m.syncStats.ToHeight, 10) + pad + "\n\n" +
			"Syncer Height: " + strconv.FormatUint(m.syncStats.Height, 10)
	}

	return syncerPanel
}

func (m *model) daserPanel(frameHeight int) string {
	var daserPanel string
	pad := strings.Repeat(" ", padding)
	if m.currentStats == nil {
		daserPanel = "\n" +
			pad + m.samplingProgress.View()
	}

	var workerString string
	if m.currentStats != nil {
		sort.Slice(m.currentStats.Workers, func(i, j int) bool {
			return m.currentStats.Workers[i].From < m.currentStats.Workers[j].From
		})
		workerString = "Workers: \n"
		for _, worker := range m.currentStats.Workers {
			progressBar := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())
			workerString += pad + strconv.FormatUint(worker.From, 10) + pad + progressBar.ViewAs(float64(worker.Curr-worker.From)/float64(worker.To-worker.From)) + pad + strconv.FormatUint(worker.To, 10) + "\n"
		}
		workerString += "\n\n"

		m.samplingProgress.Width = (2 * m.width / 3) - frameHeight - 4*padding - 1 - len(strconv.FormatUint(m.currentStats.NetworkHead, 10))
		daserPanel = "DASer Progress: \n" +
			pad + "0" + pad + m.samplingProgress.View() + pad + strconv.FormatUint(m.currentStats.NetworkHead, 10) + pad + "\n\n" +
			pad + "Sampled Chain Head: " + strconv.FormatUint(m.currentStats.SampledChainHead, 10) + "\n" +
			pad + "Network Head: " + strconv.FormatUint(m.currentStats.NetworkHead, 10) + "\n" +
			pad + "Head of Catchup: " + strconv.FormatUint(m.currentStats.CatchupHead, 10) + "\n\n" +
			workerString
	}

	return daserPanel
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/2, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
