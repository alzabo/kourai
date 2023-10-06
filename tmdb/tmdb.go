package kourai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type MovieSearchResults struct {
	Results      []MovieSearchResult
	Page         int
	TotalPages   int
	TotalResults int
}

// TODO: genre IDs
type MovieSearchResult struct {
	Title string
	Adult bool
	ID    int64

	// TODO: some other type here, Custom Unmarshal function
	OriginalLanguage string
	OriginalTitle    string
	Overview         string
	Popularity       float64

	// TODO: proper date
	ReleaseDate string
	VoteAverage float64
	VoteCount   int64
}

type TMDB struct {
	key     string
	baseUrl string
	http    *http.Client
}

func (t *TMDB) Get(url string) *http.Request {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("accept", "application/json")
	return req
}

// TODO: Return results from n+1 pages
func (t *TMDB) SearchMovies(title string, done <-chan struct{}, options map[string]string) (<-chan MovieSearchResult, <-chan error) {
	c := make(chan MovieSearchResult)
	errc := make(chan error, 1)
	errs := []error{}

	params := []string{
		fmt.Sprintf("query=%s", url.QueryEscape(title)),
		"include_adult=true",
	}
	for k, v := range options {
		params = append(params, fmt.Sprintf("%s=%s", k, url.QueryEscape(v)))
	}

	searchUrl, _ := url.JoinPath(t.baseUrl, "3/search/movie")
	searchUrl += fmt.Sprintf("?api_key=%s", t.key)
	searchUrl += "&" + strings.Join(params, "&")

	req := t.Get(searchUrl)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		errs = append(errs, err)
	} else {
		defer res.Body.Close()
	}

	// check the status code res.StatusCode
	var body []byte
	if b, err := io.ReadAll(res.Body); err != nil {
		errs = append(errs, err)
	} else {
		body = b
	}
	//fmt.Println(string(body))

	var movies MovieSearchResults
	if err := json.Unmarshal(body, &movies); err != nil {
		errs = append(errs, err)
	}

	if len(movies.Results) == 0 {
		errs = append(errs, fmt.Errorf("no results found at tmdb for title: \"%s\" with options %v", title, options))
	}

	go func() {
		defer close(c)
		for _, r := range movies.Results {
			//fmt.Printf("before read: searched title %s with options %v; found %v", title, options, r)
			select {
			case c <- r:
				//fmt.Printf("searched title %s with options %v; found %v\n", title, options, r)
			case <-done:
				return
			}
		}
	}()
	errc <- errors.Join(errs...)
	return c, errc
}

func (t *TMDB) SearchMovie(title string, options map[string]string) (MovieSearchResult, error) {
	done := make(chan struct{})
	defer close(done)
	movies, errc := t.SearchMovies(title, nil, options)
	if err := <-errc; err != nil {
		return MovieSearchResult{}, err
	}

	// TODO: match for more than 1 movie
	return <-movies, nil
}

func New(k string) *TMDB {
	t := TMDB{
		key:     k,
		baseUrl: "https://api.themoviedb.org",
	}
	return &t
}
