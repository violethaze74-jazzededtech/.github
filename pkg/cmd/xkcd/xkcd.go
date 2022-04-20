package xkcd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/ioutil"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/spf13/cobra"
	"golang.org/x/image/draw"
)

type XkcdOptions struct {
	IO *iostreams.IOStreams

	Number       string
	Scale        int
	DefaultScale bool
	Cache        bool
}

func NewCmdXkcd(f *cmdutil.Factory, runF func(*XkcdOptions) error) *cobra.Command {
	opts := &XkcdOptions{
		IO: f.IOStreams,
	}

	cmd := &cobra.Command{
		Use:   "xkcd [current | random | <comic number>]",
		Short: "Display xkcd comic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "current":
				opts.Number = ""
			case "random":
				start := time.Date(2005, time.August, 15, 0, 0, 0, 0, time.UTC)
				current := time.Now().UTC()
				days := current.Sub(start).Hours() / 24
				max := int(math.Floor(days * (3.0 / 7.0)))
				rand.Seed(time.Now().UnixNano())
				opts.Number = fmt.Sprint(rand.Intn(max))
			default:
				opts.Number = args[0]
			}

			opts.DefaultScale = !cmd.Flags().Changed("scale")

			if runF != nil {
				return runF(opts)
			}

			return xkcdRun(opts)
		},
	}

	cmd.Flags().IntVar(&opts.Scale, "scale", 2, "Scaling factor to resize comic")
	cmd.Flags().BoolVar(&opts.Cache, "cache", true, "Scaling factor to resize comic")

	return cmd
}

type xkcdComic struct {
	Day             string `json:"day"`
	Month           string `json:"month"`
	Year            string `json:"year"`
	Number          int    `json:"num"`
	Title           string `json:"title"`
	SafeTitle       string `json:"safe_title"`
	AltText         string `json:"alt"`
	ImageUrl        string `json:"img"`
	ImagePath       string
	ScaledImagePath string
}

func xkcdRun(opts *XkcdOptions) error {
	err, comic := getComic(opts.Number)
	if err != nil {
		return err
	}

	dir := os.TempDir()
	imagePath := fmt.Sprintf("%sgh_xkcd_%v.png", dir, comic.Number)
	scaledImagePath := fmt.Sprintf("%sgh_xkcd_%v_scaled.png", dir, comic.Number)
	comic.ImagePath = imagePath
	comic.ScaledImagePath = scaledImagePath
	err = downloadComic(comic)
	if err != nil {
		return err
	}

	err = scaleComic(comic, opts.Scale, opts.DefaultScale)
	if err != nil {
		return err
	}

	displayComic(comic)

	if !opts.Cache {
		time.Sleep(10 * time.Millisecond)
		os.Remove(comic.ImagePath)
		os.Remove(comic.ScaledImagePath)
	}

	return nil
}

func getComic(number string) (error, xkcdComic) {
	var url string
	comic := xkcdComic{}

	if number == "" {
		url = "http://xkcd.com/info.0.json"
	} else {
		url = fmt.Sprintf("http://xkcd.com/%s/info.0.json", number)
	}

	res, err := http.Get(url)
	if err != nil {
		return err, comic
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err, comic
	}

	err = json.Unmarshal(body, &comic)
	if err != nil {
		return err, comic
	}

	return nil, comic
}

func downloadComic(comic xkcdComic) error {
	filePath := comic.ImagePath
	if _, err := os.Stat(filePath); err == nil {
		return nil
	}

	res, err := http.Get(comic.ImageUrl)
	if err != nil {
		return err
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, res.Body)
	return err
}

// https://sw.kovidgoyal.net/kitty/graphics-protocol.html
func displayComic(comic xkcdComic) {
	fmt.Printf("Comic #%v for %s/%s/%s\n\n", comic.Number, comic.Day, comic.Month, comic.Year)
	fmt.Printf("%s\n\n", comic.Title)
	startSequence := "\033_G"
	controlData := "a=T,f=100,t=f;"
	encodedFilePath := base64.StdEncoding.EncodeToString([]byte(comic.ScaledImagePath))
	endSequence := "\033\\"
	fmt.Printf("%s%s%s%s\n\n", startSequence, controlData, encodedFilePath, endSequence)
	fmt.Println(comic.AltText)
}

func scaleComic(comic xkcdComic, scale int, defaultScale bool) error {
	filePath := comic.ScaledImagePath
	if _, err := os.Stat(filePath); err == nil && defaultScale {
		return nil
	}

	file, err := os.Open(comic.ImagePath)
	if err != nil {
		return err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return err
	}

	currentWidth := img.Bounds().Max.X
	currentLength := img.Bounds().Max.Y
	rect := image.Rect(0, 0, currentWidth*scale, currentLength*scale)
	scaled := image.NewRGBA(rect)
	draw.CatmullRom.Scale(scaled, rect, img, img.Bounds(), draw.Over, nil)

	scaledFile, err := os.Create(comic.ScaledImagePath)
	if err != nil {
		return err
	}
	defer scaledFile.Close()

	err = png.Encode(scaledFile, scaled)
	return err
}
