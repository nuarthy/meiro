package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

func TestItemMenuOpensAndClosesOnItsItem(t *testing.T) {
	var m itemMenu
	if m.opened("a") || m.owns("a") {
		t.Fatal("a fresh menu belongs to an item")
	}
	m.show("a", false, 5, 6)
	if !m.opened("a") || m.opened("b") || !m.owns("a") {
		t.Fatalf("menu not open on a only: %+v", m)
	}
	m.close()
	if m.opened("a") || !m.owns("a") {
		t.Fatalf("closing should keep the item for its exit: %+v", m)
	}
}

func TestItemMenuAnchorsAtThePointerOnlyWhenPointerOpened(t *testing.T) {
	var m itemMenu
	m.show("a", true, 12, 34)
	if !m.atPointer || m.x != 12 || m.y != 34 {
		t.Fatalf("pointer anchor lost: %+v", m)
	}
	// A later menu-button open on another item forgets the pointer, and keeps
	// the old coordinates from leaking into its anchor choice.
	m.show("b", false, 99, 99)
	if m.atPointer || m.x != 12 || m.y != 34 {
		t.Fatalf("button open kept pointer anchor: %+v", m)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		if _, ok := m.pointerAnchor(c, "b"); ok {
			t.Error("button-opened menu has a pointer anchor")
		}
		m.show("a", true, 1, 2)
		if _, ok := m.pointerAnchor(c, "b"); ok {
			t.Error("pointer anchor offered to another item")
		}
		if _, ok := m.pointerAnchor(c, "a"); !ok {
			t.Error("pointer anchor missing on its item")
		}
	}, 100, 100)
	tt.Frame()
}

func TestThemesAreIsolatedPerWindow(t *testing.T) {
	var wg sync.WaitGroup
	for i, seed := range []string{"#d81b78", "#1b78d8", "#78d81b", "#6750a4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			color, _ := parseSeed(seed)
			want := m3.New(m3.Config{Seed: color, Font: "Font" + string(rune('A'+i))}, false)
			var got *m3.Theme
			tt := ui.NewTester(func(c *ui.Context) {
				m3.Provide(c, want)
				got = m3.Of(c)
			}, 100, 100)
			for range 20 {
				tt.Frame()
				if got != want || got.UI(ui.LightTheme()).Font != want.Font {
					t.Errorf("window %d saw another window's theme", i)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// shuffleMenuLibrary serves a library holding one playlist card, and the
// playlist that card reads.
type shuffleMenuLibrary struct{}

func (shuffleMenuLibrary) RoundTrip(request *http.Request) (*http.Response, error) {
	body := ""
	if request.Body != nil {
		data, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(strings.NewReader(string(data)))
		body = string(data)
	}
	send := func(text string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(text)),
			Request:    request,
		}, nil
	}
	switch {
	case strings.Contains(body, "VLPLshuffle"):
		return send(shufflePlaylistBody)
	case strings.Contains(body, "FEmusic_library_landing"):
		return send(shuffleLibraryBody)
	}
	return fakeMusic{}.RoundTrip(request)
}

// shuffleLibraryBody holds one playlist card.
const shuffleLibraryBody = `{"contents":{"musicCarouselShelfRenderer":{"contents":[
	{"musicTwoRowItemRenderer":{
		"title":{"runs":[{"text":"Shuffled Mix"}]},
		"subtitle":{"runs":[{"text":"Playlist"}]},
		"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLshuffle"}}
	}}
]}}}`

// shufflePlaylistBody is the playlist that card reads, three songs long.
const shufflePlaylistBody = `{"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"one","title":{"simpleText":"One"},"lengthText":{"simpleText":"1:00"}}},
	{"playlistVideoRenderer":{"videoId":"two","title":{"simpleText":"Two"},"lengthText":{"simpleText":"2:00"}}},
	{"playlistVideoRenderer":{"videoId":"three","title":{"simpleText":"Three"},"lengthText":{"simpleText":"3:00"}}}
]}}}`

// The menu of a playlist card offers shuffle play, which puts the whole
// playlist on from a song drawn at random.
func TestAPlaylistCardMenuOffersShufflePlay(t *testing.T) {
	a := newTestApp()
	client := youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: shuffleMenuLibrary{}}})
	a.public, a.authed, a.signedIn = client, client, true
	tt := ui.NewTester(a.view, 1000, 700)
	a.router.Push("/library")
	for i := 0; i < 20 && len(a.feed.sections) == 0; i++ {
		tt.Frame()
	}
	tt.Frame()
	card, ok := tt.Find("Shuffled Mix")
	if !ok {
		t.Fatalf("the library has no playlist card: %q", tt.Texts())
	}
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	if err := tt.Click("More options for Shuffled Mix"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Shuffle play") {
		t.Fatalf("the playlist menu does not offer shuffle play: %q", tt.Texts())
	}
	if err := tt.Click("Shuffle play"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if !a.shuffle {
		t.Error("shuffle play left shuffle off")
	}
	if len(a.queue) < 3 {
		t.Fatalf("shuffle play queued %d of the playlist's 3 songs", len(a.queue))
	}
	held := map[string]int{}
	for _, item := range a.queue[:3] {
		held[item.VideoID]++
	}
	for _, id := range []string{"one", "two", "three"} {
		if held[id] != 1 {
			t.Fatalf("the queue holds the playlist as %v", held)
		}
	}
	opened := map[string]bool{a.queue[a.index].VideoID: true}
	for range 40 {
		a.playCollection(youtube.MusicItem{BrowseID: "VLPLshuffle", Title: "Shuffled Mix"}, "", 0, true)
		opened[a.queue[a.index].VideoID] = true
	}
	if len(opened) < 2 {
		t.Errorf("shuffle play always opened on %v", opened)
	}
}

// removingLibrary serves a playlist page and records the edit made from it.
type removingLibrary struct {
	body map[string]any
}

func (r *removingLibrary) RoundTrip(request *http.Request) (*http.Response, error) {
	body := ""
	if request.Body != nil {
		data, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(strings.NewReader(string(data)))
		body = string(data)
	}
	send := func(text string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text)), Request: request}, nil
	}
	switch {
	case strings.Contains(request.URL.Path, "edit_playlist"):
		_ = json.Unmarshal([]byte(body), &r.body)
		return send(`{"status":"STATUS_SUCCEEDED"}`)
	case strings.Contains(body, "VLPL_video"):
		return send(playlistResponse)
	}
	return fakeMusic{}.RoundTrip(request)
}

// A song on a playlist page comes off it, and the edit names the playlist and
// the song. The menu offers it only on a playlist page.
func TestASongInAPlaylistComesOffItFromItsMenu(t *testing.T) {
	a := newTestApp()
	transport := &removingLibrary{}
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := youtube.NewClient(youtube.Options{APIKey: "test", CookieAuth: auth, HTTPClient: &http.Client{Transport: transport}})
	a.public, a.authed, a.signedIn = client, client, true
	a.detail = detail{kind: pagePlaylist, title: "My playlist"}
	a.router.Push("/playlist/VLPL_video")
	a.removeFromPlaylist(youtube.MusicItem{Title: "Playlist video", VideoID: "playlist-video"})

	if transport.body["playlistId"] != "PL_video" {
		t.Errorf("playlistId = %#v, want the playlist without the VL prefix", transport.body["playlistId"])
	}
	actions, _ := transport.body["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("actions = %#v, want one", transport.body["actions"])
	}
	action, _ := actions[0].(map[string]any)
	if action["action"] != "ACTION_REMOVE_VIDEO" || action["removedVideoId"] != "playlist-video" {
		t.Errorf("action = %#v, want the song removed", action)
	}
	if !strings.Contains(a.notice, "Removed") {
		t.Errorf("the removal was not announced: %q", a.notice)
	}

	// A song playing from anywhere else is not offered the same treatment.
	a.router.Push("/home")
	transport.body = nil
	a.removeFromPlaylist(youtube.MusicItem{Title: "Elsewhere", VideoID: "elsewhere"})
	if transport.body != nil {
		t.Errorf("a song outside a playlist was removed anyway: %#v", transport.body)
	}
}

// searchingPage serves a playlist page of three songs.
type searchingPage struct{}

func (searchingPage) RoundTrip(request *http.Request) (*http.Response, error) {
	body := ""
	if request.Body != nil {
		data, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(strings.NewReader(string(data)))
		body = string(data)
	}
	send := func(text string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text)), Request: request}, nil
	}
	if strings.Contains(body, "VLPL_video") {
		return send(searchingPageBody)
	}
	return fakeMusic{}.RoundTrip(request)
}

const searchingPageBody = `{"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"one","title":{"simpleText":"Ameiro"},"lengthText":{"simpleText":"3:00"}}},
	{"playlistVideoRenderer":{"videoId":"two","title":{"simpleText":"Meteorite"},"lengthText":{"simpleText":"4:00"}}},
	{"playlistVideoRenderer":{"videoId":"three","title":{"simpleText":"Ameiro (Preview)"},"lengthText":{"simpleText":"1:00"}}}
]}}}`

// Searching a playlist page narrows it to the songs that match, and play acts on
// what is left. A search that matches nothing says so.
func TestAPlaylistPageCanBeSearched(t *testing.T) {
	a := newTestApp()
	client := youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: searchingPage{}}})
	a.public, a.authed, a.signedIn = client, client, true
	tt := ui.NewTester(a.view, 1000, 700)
	a.router.Push("/playlist/VLPL_video")
	for i := 0; i < 20 && len(a.playable) == 0; i++ {
		tt.Frame()
	}
	tt.Frame()
	if len(a.playable) != 3 {
		t.Fatalf("the page holds %d songs, want 3", len(a.playable))
	}
	if !tt.HasText("Search in this playlist") {
		t.Fatalf("the playlist page has no search field: %q", tt.Texts())
	}

	a.detailFilter = "ameiro"
	tt.Frame()
	tt.Frame()
	if len(a.playable) != 2 {
		t.Errorf("the search left %d songs to play, want the 2 that match", len(a.playable))
	}
	if tt.HasText("Meteorite") {
		t.Errorf("the search left a song that does not match: %q", tt.Texts())
	}

	a.detailFilter = "nothing here"
	tt.Frame()
	tt.Frame()
	if !tt.HasText("No songs match “nothing here”") {
		t.Errorf("an empty search does not say so: %q", tt.Texts())
	}

	a.detailFilter = ""
	tt.Frame()
	tt.Frame()
	if len(a.playable) != 3 {
		t.Errorf("clearing the search left %d songs, want the whole playlist", len(a.playable))
	}
}
