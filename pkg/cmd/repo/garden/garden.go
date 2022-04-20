package garden

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/cli/cli/api"
	"github.com/cli/cli/internal/ghinstance"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/utils"
	"github.com/spf13/cobra"
)

type Geometry struct {
	Width      int
	Height     int
	Density    float64
	Repository ghrepo.Interface
}

type Player struct {
	X                   int
	Y                   int
	Char                string
	Geo                 *Geometry
	ShoeMoistureContent int
}

type Commit struct {
	Email  string
	Handle string
	Sha    string
	Char   string
}

type Cell struct {
	Char       string
	StatusLine string
}

const (
	DirUp = iota
	DirDown
	DirLeft
	DirRight
)

type Direction = int

func (p *Player) move(direction Direction) bool {
	switch direction {
	case DirUp:
		if p.Y == 0 {
			return false
		}
		p.Y--
	case DirDown:
		if p.Y == p.Geo.Height-1 {
			return false
		}
		p.Y++
	case DirLeft:
		if p.X == 0 {
			return false
		}
		p.X--
	case DirRight:
		if p.X == p.Geo.Width-1 {
			return false
		}
		p.X++
	}

	return true
}

type GardenOptions struct {
	HttpClient func() (*http.Client, error)
	IO         *iostreams.IOStreams
	BaseRepo   func() (ghrepo.Interface, error)

	RepoArg string
}

func NewCmdGarden(f *cmdutil.Factory, runF func(*GardenOptions) error) *cobra.Command {
	opts := GardenOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
		BaseRepo:   f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:    "garden [<repository>]",
		Short:  "Explore a git repository as a garden",
		Long:   "Use arrow keys, WASD or vi keys to move. q to quit.",
		Hidden: true,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.RepoArg = args[0]
			}
			if runF != nil {
				return runF(&opts)
			}
			return gardenRun(&opts)
		},
	}

	return cmd
}

func gardenRun(opts *GardenOptions) error {
	cs := opts.IO.ColorScheme()
	out := opts.IO.Out

	if runtime.GOOS == "windows" {
		return errors.New("sorry :( this command only works on linux and macos")
	}

	if !opts.IO.IsStdoutTTY() {
		return errors.New("must be connected to a terminal")
	}

	httpClient, err := opts.HttpClient()
	if err != nil {
		return err
	}

	var toView ghrepo.Interface
	apiClient := api.NewClientFromHTTP(httpClient)
	if opts.RepoArg == "" {
		var err error
		toView, err = opts.BaseRepo()
		if err != nil {
			return err
		}
	} else {
		var err error
		viewURL := opts.RepoArg
		if !strings.Contains(viewURL, "/") {
			currentUser, err := api.CurrentLoginName(apiClient, ghinstance.Default())
			if err != nil {
				return err
			}
			viewURL = currentUser + "/" + viewURL
		}
		toView, err = ghrepo.FromFullName(viewURL)
		if err != nil {
			return fmt.Errorf("argument error: %w", err)
		}
	}

	seed := computeSeed(ghrepo.FullName(toView))
	rand.Seed(seed)

	termWidth, termHeight, err := utils.TerminalSize(out)
	if err != nil {
		return err
	}

	termWidth -= 10
	termHeight -= 10

	geo := &Geometry{
		Width:      termWidth,
		Height:     termHeight,
		Repository: toView,
		// TODO based on number of commits/cells instead of just hardcoding
		Density: 0.3,
	}

	maxCommits := (geo.Width * geo.Height) / 2

	sttyFileArg := "-F"
	if runtime.GOOS == "darwin" {
		sttyFileArg = "-f"
	}

	oldTTYCommand := exec.Command("stty", sttyFileArg, "/dev/tty", "-g")
	oldTTYSettings, err := oldTTYCommand.CombinedOutput()
	if err != nil {
		fmt.Fprintln(out, "getting TTY settings failed:", string(oldTTYSettings))
		return err
	}

	opts.IO.StartProgressIndicator()
	fmt.Fprintln(out, "gathering commits; this could take a minute...")
	commits, err := getCommits(httpClient, toView, maxCommits)
	opts.IO.StopProgressIndicator()
	if err != nil {
		return err
	}
	player := &Player{0, 0, cs.Bold("@"), geo, 0}

	garden := plantGarden(commits, geo)
	if len(garden) < geo.Height {
		geo.Height = len(garden)
	}
	if geo.Height > 0 && len(garden[0]) < geo.Width {
		geo.Width = len(garden[0])
	} else if len(garden) == 0 {
		geo.Width = 0
	}
	clear(opts.IO)
	drawGarden(opts.IO, garden, player)

	// thanks stackoverflow https://stackoverflow.com/a/17278776
	_ = exec.Command("stty", sttyFileArg, "/dev/tty", "cbreak", "min", "1").Run()
	_ = exec.Command("stty", sttyFileArg, "/dev/tty", "-echo").Run()

	walkAway := func() {
		clear(opts.IO)
		fmt.Fprint(out, "\033[?25h")
		_ = exec.Command("stty", sttyFileArg, "/dev/tty", strings.TrimSpace(string(oldTTYSettings))).Run()
		fmt.Fprintln(out)
		fmt.Fprintln(out, cs.Bold("You turn and walk away from the wildflower garden..."))
	}

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		walkAway()
		os.Exit(0)
	}()

	var b []byte = make([]byte, 3)
	for {
		_, _ = opts.IO.In.Read(b)

		oldX := player.X
		oldY := player.Y
		moved := false
		quitting := false
		continuing := false

		switch {
		case isLeft(b):
			moved = player.move(DirLeft)
		case isRight(b):
			moved = player.move(DirRight)
		case isUp(b):
			moved = player.move(DirUp)
		case isDown(b):
			moved = player.move(DirDown)
		case isQuit(b):
			quitting = true
		default:
			continuing = true
		}

		if quitting {
			break
		}

		if !moved || continuing {
			continue
		}

		underPlayer := garden[player.Y][player.X]
		previousCell := garden[oldY][oldX]

		// print whatever was just under player

		fmt.Fprint(out, "\033[;H") // move to top left
		for x := 0; x < oldX && x < player.Geo.Width; x++ {
			fmt.Fprint(out, "\033[C")
		}
		for y := 0; y < oldY && y < player.Geo.Height; y++ {
			fmt.Fprint(out, "\033[B")
		}
		fmt.Fprint(out, previousCell.Char)

		// print player character
		fmt.Fprint(out, "\033[;H") // move to top left
		for x := 0; x < player.X && x < player.Geo.Width; x++ {
			fmt.Fprint(out, "\033[C")
		}
		for y := 0; y < player.Y && y < player.Geo.Height; y++ {
			fmt.Fprint(out, "\033[B")
		}
		fmt.Fprint(out, player.Char)

		// handle stream wettening

		if strings.Contains(underPlayer.StatusLine, "stream") {
			player.ShoeMoistureContent = 5
		} else {
			if player.ShoeMoistureContent > 0 {
				player.ShoeMoistureContent--
			}
		}

		// status line stuff
		sl := statusLine(garden, player, opts.IO)

		fmt.Fprint(out, "\033[;H") // move to top left
		for y := 0; y < player.Geo.Height-1; y++ {
			fmt.Fprint(out, "\033[B")
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out)

		fmt.Fprint(out, cs.Bold(sl))
	}

	walkAway()
	return nil
}

func isLeft(b []byte) bool {
	left := []byte{27, 91, 68}
	r := rune(b[0])
	return bytes.EqualFold(b, left) || r == 'a' || r == 'h'
}

func isRight(b []byte) bool {
	right := []byte{27, 91, 67}
	r := rune(b[0])
	return bytes.EqualFold(b, right) || r == 'd' || r == 'l'
}

func isDown(b []byte) bool {
	down := []byte{27, 91, 66}
	r := rune(b[0])
	return bytes.EqualFold(b, down) || r == 's' || r == 'j'
}

func isUp(b []byte) bool {
	up := []byte{27, 91, 65}
	r := rune(b[0])
	return bytes.EqualFold(b, up) || r == 'w' || r == 'k'
}

func isQuit(b []byte) bool {
	return rune(b[0]) == 'q'
}

func plantGarden(commits []*Commit, geo *Geometry) [][]*Cell {
	cellIx := 0
	grassCell := &Cell{RGB(0, 200, 0, ","), "You're standing on a patch of grass in a field of wildflowers."}
	garden := [][]*Cell{}
	streamIx := rand.Intn(geo.Width - 1)
	if streamIx == geo.Width/2 {
		streamIx--
	}
	tint := 0
	for y := 0; y < geo.Height; y++ {
		if cellIx == len(commits)-1 {
			break
		}
		garden = append(garden, []*Cell{})
		for x := 0; x < geo.Width; x++ {
			if (y > 0 && (x == 0 || x == geo.Width-1)) || y == geo.Height-1 {
				garden[y] = append(garden[y], &Cell{
					Char:       RGB(0, 150, 0, "^"),
					StatusLine: "You're standing under a tall, leafy tree.",
				})
				continue
			}
			if x == streamIx {
				garden[y] = append(garden[y], &Cell{
					Char:       RGB(tint, tint, 255, "#"),
					StatusLine: "You're standing in a shallow stream. It's refreshing.",
				})
				tint += 15
				streamIx--
				if rand.Float64() < 0.5 {
					streamIx++
				}
				if streamIx < 0 {
					streamIx = 0
				}
				if streamIx > geo.Width {
					streamIx = geo.Width
				}
				continue
			}
			if y == 0 && (x < geo.Width/2 || x > geo.Width/2) {
				garden[y] = append(garden[y], &Cell{
					Char:       RGB(0, 200, 0, ","),
					StatusLine: "You're standing by a wildflower garden. There is a light breeze.",
				})
				continue
			} else if y == 0 && x == geo.Width/2 {
				garden[y] = append(garden[y], &Cell{
					Char:       RGB(139, 69, 19, "+"),
					StatusLine: fmt.Sprintf("You're standing in front of a weather-beaten sign that says %s.", ghrepo.FullName(geo.Repository)),
				})
				continue
			}

			if cellIx == len(commits)-1 {
				garden[y] = append(garden[y], grassCell)
				continue
			}

			chance := rand.Float64()
			if chance <= geo.Density {
				commit := commits[cellIx]
				garden[y] = append(garden[y], &Cell{
					Char:       commits[cellIx].Char,
					StatusLine: fmt.Sprintf("You're standing at a flower called %s planted by %s.", commit.Sha[0:6], commit.Handle),
				})
				cellIx++
			} else {
				garden[y] = append(garden[y], grassCell)
			}
		}
	}

	return garden
}

func drawGarden(io *iostreams.IOStreams, garden [][]*Cell, player *Player) {
	out := io.Out
	cs := io.ColorScheme()

	fmt.Fprint(out, "\033[?25l") // hide cursor. it needs to be restored at command exit.
	sl := ""
	for y, gardenRow := range garden {
		for x, gardenCell := range gardenRow {
			char := ""
			underPlayer := (player.X == x && player.Y == y)
			if underPlayer {
				sl = gardenCell.StatusLine
				char = cs.Bold(player.Char)

				if strings.Contains(gardenCell.StatusLine, "stream") {
					player.ShoeMoistureContent = 5
				}
			} else {
				char = gardenCell.Char
			}

			fmt.Fprint(out, char)
		}
		fmt.Fprintln(out)
	}

	fmt.Println()
	fmt.Fprintln(out, cs.Bold(sl))
}

func statusLine(garden [][]*Cell, player *Player, io *iostreams.IOStreams) string {
	width := io.TerminalWidth()
	statusLines := []string{garden[player.Y][player.X].StatusLine}

	if player.ShoeMoistureContent > 1 {
		statusLines = append(statusLines, "Your shoes squish with water from the stream.")
	} else if player.ShoeMoistureContent == 1 {
		statusLines = append(statusLines, "Your shoes seem to have dried out.")
	} else {
		statusLines = append(statusLines, "")
	}

	for i, line := range statusLines {
		if len(line) < width {
			paddingSize := width - len(line)
			statusLines[i] = line + strings.Repeat(" ", paddingSize)
		}
	}

	return strings.Join(statusLines, "\n")
}

func shaToColorFunc(sha string) func(string) string {
	return func(c string) string {
		red, err := strconv.ParseInt(sha[0:2], 16, 64)
		if err != nil {
			panic(err)
		}

		green, err := strconv.ParseInt(sha[2:4], 16, 64)
		if err != nil {
			panic(err)
		}

		blue, err := strconv.ParseInt(sha[4:6], 16, 64)
		if err != nil {
			panic(err)
		}

		return fmt.Sprintf("\033[38;2;%d;%d;%dm%s\033[0m", red, green, blue, c)
	}
}

func computeSeed(seed string) int64 {
	lol := ""

	for _, r := range seed {
		lol += fmt.Sprintf("%d", int(r))
	}

	result, err := strconv.ParseInt(lol[0:10], 10, 64)
	if err != nil {
		panic(err)
	}

	return result
}

func clear(io *iostreams.IOStreams) {
	cmd := exec.Command("clear")
	cmd.Stdout = io.Out
	_ = cmd.Run()
}

func RGB(r, g, b int, x string) string {
	return fmt.Sprintf("\033[38;2;%d;%d;%dm%s\033[0m", r, g, b, x)
}
