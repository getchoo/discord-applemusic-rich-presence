package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/cheshir/ttlcache"
	"github.com/hugolgst/rich-go/client"
)

const statePlaying = "playing"

var (
	shortSleep    = 5 * time.Second
	longSleep     = time.Minute
	songCache     = ttlcache.New(time.Minute)
	artworkCache  = ttlcache.New(time.Minute)
	shareURLCache = ttlcache.New(time.Minute)
	artistCache   = ttlcache.New(time.Minute)
)

func main() {
	defer func() {
		_ = songCache.Close()
		_ = artworkCache.Close()
		_ = shareURLCache.Close()
	}()

	log.SetLevel(log.WarnLevel)
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		log.SetLevel(log.ParseLevel(level))
	}

	log.ErrorLevelStyle = lipgloss.NewStyle().
		SetString("error").
		Foreground(lipgloss.Color("#ed8796")).Width(6)
	log.WarnLevelStyle = lipgloss.NewStyle().
		SetString("warn").
		Foreground(lipgloss.Color("#eed49f")).Width(6)
	log.InfoLevelStyle = lipgloss.NewStyle().
		SetString("info").
		Foreground(lipgloss.Color("#8aadf4")).Width(6)
	log.DebugLevelStyle = lipgloss.NewStyle().
		SetString("debug").
		Foreground(lipgloss.Color("#c6a0f6")).Width(6)
	log.SetReportTimestamp(false)

	ac := activityConnection{}
	defer func() { ac.stop() }()

	for {
		if !isRunning("Music") {
			log.Warn("Apple Music is not running", "sleep", longSleep)
			ac.stop()
			time.Sleep(longSleep)
			continue
		}

		if !isRunning("Discord") && !isRunning("Vesktop") {
			log.Warn("Discord is not running", "sleep", longSleep)
			ac.stop()
			time.Sleep(longSleep)
			continue
		}

		details, err := getNowPlaying()

		if err != nil {
			if strings.Contains(err.Error(), "(-1728)") {
				log.Warn("Apple Music stopped running", "sleep", longSleep)
				ac.stop()
				time.Sleep(longSleep)
				continue
			}

			log.Error("will try again soon", "err", err, "sleep", shortSleep)
			ac.stop()
			time.Sleep(shortSleep)
			continue
		}

		if details.State != statePlaying {
			if ac.connected {
				log.Info("not playing")
				ac.stop()
			}
			time.Sleep(shortSleep)
			continue
		}

		if err := ac.play(details); err != nil {
			log.Warn("could not set activity, will retry later", "err", err)
		}

		time.Sleep(shortSleep)
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func isRunning(app string) bool {
	bts, err := exec.Command("pgrep", "-f", "MacOS/"+app).CombinedOutput()
	return string(bts) != "" && err == nil
}

func tellMusic(s string) (string, error) {
	bts, err := exec.Command(
		"osascript",
		"-e", "tell application \"Music\"",
		"-e", s,
		"-e", "end tell",
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(bts)), err)
	}
	return strings.TrimSpace(string(bts)), nil
}

func getNowPlaying() (Details, error) {
	init := time.Now()
	defer func() {
		log.Info("got info", "took", time.Since(init))
	}()

	initialState, err := tellMusic("get {database id} of current track & {player position, player state}")
	if err != nil {
		return Details{}, err
	}

	songID, err := strconv.ParseInt(strings.Split(initialState, ", ")[0], 10, 64)
	if err != nil {
		return Details{}, err
	}

	position, err := strconv.ParseFloat(strings.Split(initialState, ", ")[1], 64)
	if err != nil {
		return Details{}, err
	}

	state := strings.Split(initialState, ", ")[2]
	if state != statePlaying {
		return Details{
			State: state,
		}, nil
	}

	cached, ok := songCache.Get(ttlcache.Int64Key(songID))
	if ok {
		log.Debug("got song from cache", "songID", songID)
		return Details{
			Song:     cached.(Song),
			Position: position,
			State:    state,
		}, nil
	}

	name, err := tellMusic("get {name} of current track")
	if err != nil {
		return Details{}, err
	}
	artist, err := tellMusic("get {artist} of current track")
	if err != nil {
		return Details{}, err
	}
	album, err := tellMusic("get {album} of current track")
	if err != nil {
		return Details{}, err
	}
	yearDuration, err := tellMusic("get {year, duration} of current track")
	if err != nil {
		return Details{}, err
	}

	year, err := strconv.Atoi(strings.Split(yearDuration, ", ")[0])
	if err != nil {
		return Details{}, err
	}

	duration, err := strconv.ParseFloat(strings.Split(yearDuration, ", ")[1], 64)
	if err != nil {
		return Details{}, err
	}

	metadata, err := getMetadata(artist, album, name)
	if err != nil {
		return Details{}, err
	}

	song := Song{
		ID:            songID,
		Name:          name,
		Artist:        artist,
		Album:         album,
		Year:          year,
		Duration:      duration,
		Artwork:       metadata.Artwork,
		ArtistArtwork: metadata.ArtistArtwork,
		ShareURL:      metadata.ShareURL,
		ShareID:       metadata.ID,
	}

	songCache.Set(ttlcache.Int64Key(songID), song, 24*time.Hour)

	return Details{
		Song:     song,
		Position: position,
		State:    state,
	}, nil
}

type Details struct {
	Song     Song
	Position float64
	State    string
}

type Song struct {
	ID            int64
	Name          string
	Artist        string
	Album         string
	Year          int
	Duration      float64
	Artwork       string
	ArtistArtwork string
	ShareURL      string
	ShareID       string
}

func getMetadata(artist, album, song string) (Metadata, error) {
	key := url.QueryEscape(strings.Join([]string{artist, album, song}, " "))
	artworkCached, artworkOk := artworkCache.Get(ttlcache.StringKey(key))
	shareURLCached, shareURLOk := shareURLCache.Get(ttlcache.StringKey(key))
	artistCached, artistOk := artistCache.Get(ttlcache.StringKey(key))

	if artworkOk && shareURLOk && artistOk {
		log.Debug("got album and artist artwork from cache", "key", key)
		return Metadata{
			Artwork:       artworkCached.(string),
			ShareURL:      shareURLCached.(string),
			ArtistArtwork: artistCached.(string),
		}, nil
	}

	baseURL := "https://tools.applemediaservices.com/api/apple-media/music/US/search.json?types=songs&limit=1"
	resp, err := http.Get(baseURL + "&term=" + key)
	if err != nil {
		return Metadata{}, err
	}
	defer resp.Body.Close()

	bts, err := io.ReadAll(resp.Body)
	if err != nil {
		return Metadata{}, err
	}

	var result getSongMetadataResult
	if err := json.Unmarshal(bts, &result); err != nil {
		return Metadata{}, err
	}
	if len(result.Songs.Data) == 0 {
		return Metadata{}, nil
	}

	id := result.Songs.Data[0].ID
	artwork := result.Songs.Data[0].Attributes.Artwork.URL
	artwork = strings.Replace(artwork, "{w}", "512", 1)
	artwork = strings.Replace(artwork, "{h}", "512", 1)
	shareURL := result.Songs.Data[0].Attributes.URL

	artistMetadataURL := "https://tools.applemediaservices.com/api/apple-media/music/US/search.json?types=artists&limit=1&term=" + url.QueryEscape(trySplit(artist, []string{",", "&"})[0])
	respArtist, err := http.Get(artistMetadataURL)
	if err != nil {
		return Metadata{}, err
	}
	defer respArtist.Body.Close()

	btsArtist, err := io.ReadAll(respArtist.Body)
	if err != nil {
		return Metadata{}, err
	}

	var artistResult getArtistMetadataResult
	if err := json.Unmarshal(btsArtist, &artistResult); err != nil {
		return Metadata{}, err
	}

	var artistArtwork string

	if len(artistResult.Artists.Data) > 0 {
		artistArtwork = artistResult.Artists.Data[0].Attributes.Artwork.URL
		artistArtwork = strings.Replace(artistArtwork, "{w}", "512", 1)
		artistArtwork = strings.Replace(artistArtwork, "{h}", "512", 1)
	}

	artworkCache.Set(ttlcache.StringKey(key), artwork, time.Hour)
	shareURLCache.Set(ttlcache.StringKey(key), shareURL, time.Hour)
	artistCache.Set(ttlcache.StringKey(key), artistArtwork, time.Hour)

	return Metadata{
		ID:            id,
		Artwork:       artwork,
		ShareURL:      shareURL,
		ArtistArtwork: artistArtwork,
	}, nil
}

type getSongMetadataResult struct {
	Songs struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				URL     string `json:"url"`
				Artwork struct {
					URL string `json:"url"`
				} `json:"artwork"`
			} `json:"attributes"`
		} `json:"data"`
	} `json:"songs"`
}

type getArtistMetadataResult struct {
	Artists struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Artwork struct {
					URL string `json:"url"`
				} `json:"artwork"`
			} `json:"attributes"`
		} `json:"data"`
	} `json:"artists"`
}

type Metadata struct {
	ID            string
	Artwork       string
	ArtistArtwork string
	ShareURL      string
}

type activityConnection struct {
	connected    bool
	lastSongID   int64
	lastPosition float64
}

func (ac *activityConnection) stop() {
	if ac.connected {
		client.Logout()
		ac.connected = false
		ac.lastPosition = 0.0
		ac.lastSongID = 0
	}
}

func (ac *activityConnection) play(details Details) error {
	song := details.Song

	if ac.lastSongID == song.ID {
		if details.Position >= ac.lastPosition {
			log.
				Debug("ongoing activity, ignoring", "songID", song.ID, "position", details.Position)
			return nil
		}
	}

	log.
		Debug("new event", "lastSongID", ac.lastSongID, "songID", song.ID, "lastPosition", ac.lastPosition, "position", details.Position)

	ac.lastPosition = details.Position
	ac.lastSongID = song.ID

	start := time.Now().Add(-1 * time.Duration(details.Position) * time.Second)
	end := time.Now().Add(time.Duration(song.Duration-details.Position) * time.Second)

	if !ac.connected {
		if err := client.Login("861702238472241162"); err != nil {
			log.Fatal("could not create rich presence client", "err", err)
		}
		ac.connected = true
	}

	var buttons []*client.Button
	if song.ShareURL != "" {
		buttons = append(buttons, &client.Button{
			Label: "Listen on Apple Music",
			Url:   song.ShareURL,
		})
	}

	if link := songlink(song); link != "" {
		buttons = append(buttons, &client.Button{
			Label: "View on SongLink",
			Url:   link,
		})
	}

	if err := client.SetActivity(client.Activity{
		Details:    fmt.Sprintf("%s · %s", song.Name, song.Artist),
		State:      song.Album,
		LargeImage: firstNonEmpty(song.Artwork, "applemusic"),
		SmallImage: firstNonEmpty(song.ArtistArtwork, "play"),
		LargeText:  song.Name,
		SmallText:  song.Artist,
		Timestamps: &client.Timestamps{
			Start: timePtr(start),
			End:   timePtr(end),
		},
		Buttons: buttons,
	}); err != nil {
		return err
	}

	log.
		Warn("now playing", "song", song.Name, "album", song.Album, "artist", song.Artist, "year", song.Year, "duration", time.Duration(song.Duration)*time.Second, "position", time.Duration(details.Position)*time.Second, "songlink", songlink(song))

	return nil
}

func songlink(song Song) string {
	if song.ShareID == "" {
		return ""
	}

	return fmt.Sprintf("https://song.link/i/%s", song.ShareID)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}

	return ""
}

func trySplit(s string, seps []string) []string {
	for _, sep := range seps {
		parts := strings.Split(s, sep)
		if len(parts) > 1 {
			return parts
		}
	}

	return []string{s}
}
