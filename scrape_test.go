package parallel

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func ExampleFunc() {
	// get all the links in the Go blog, then in all the pages those pages link to, up to 3 levels
	// deep.
	links := Scrape(3, "https://blog.golang.org/examples")

	fmt.Printf("links %d: %v", len(links), links)
}

// HTMLLinks is a more real-world example of how Func can be used. It fetches the URLs given in
// parallel and returns all the unique links found.
//
// Note how gathering results from
func HTMLLinks(urls ...string) (links StringSet, err error) {
	nSites := len(urls)

	// we want to get a list of links from each of the sites in urls.
	//
	// Reserve space for each of their link sets them up front. This way each goroutine has its own
	// "slot" in results that it can write to without worrying about concurrent access.
	//
	// This trades off memory (might be duplicates between sites, and there's nSites StringSets
	// instead of just one)
	results := make([]StringSet, nSites)

	// We want to iterate over urls in parallel, fetching the HTML and parsing the links from each.
	// This is the func that does the work. idx will go from 0 to nSites
	fn := Func(func(idx int) error {
		// reading urls is OK since nobody's modifying it concurrently
		r, err := http.Get(urls[idx])
		if err != nil {
			return err
		}
		defer r.Body.Close()
		doc, err := html.Parse(r.Body)
		if err != nil {
			return err
		}

		// writing to results[idx] without synchronization is OK since this is the only goroutine
		// that's writing to that location
		results[idx] = parseLinks(doc, results[idx])
		return nil
	})

	// run fn a total of nSites times. Run at most 5 concurrently.
	// Blocks until all are finished, and returns a multierror (go.uber.org/multierr) of all the errors
	// returned
	err = fn.Do(nSites, 5)

	// return unique links only
	var resultSet StringSet
	for _, res := range results {
		resultSet.AddSet(res)
	}
	return resultSet, err
}

func Scrape(maxDepth int, urls ...string) (links StringSet) {
	if maxDepth == 0 {
		return
	}
	// even if there was an error we might still have gotten some links
	links, _ = HTMLLinks(urls...)
	// remove the original urls from found links
	links.Remove(urls...)

	// scrape the links we got
	more := Scrape(maxDepth-1, links.List()...)

	return links.AddSet(more)
}

// note: everything below here is just utilities

func parseLinks(n *html.Node, found StringSet) StringSet {
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" && strings.HasPrefix(a.Val,
				"http:") || strings.HasPrefix(a.Val, "https:") {
				u, err := url.Parse(a.Val)
				if err != nil {
					break
				}
				u.RawQuery = ""
				u.RawFragment = ""
				u.Fragment = ""
				found.Add(u.String())
				break
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		found = parseLinks(c, found)
	}

	return found
}

type StringSet map[string]struct{}

func (s *StringSet) Add(strs ...string) *StringSet {
	if len(*s) == 0 {
		*s = make(StringSet, len(strs))
	}

	for _, h := range strs {
		(*s)[h] = struct{}{}
	}

	return s
}

func (s *StringSet) AddSet(strs StringSet) StringSet {
	if len(*s) == 0 {
		*s = make(StringSet, len(strs))
	}

	for v := range strs {
		(*s)[v] = struct{}{}
	}

	return *s
}

func (s *StringSet) Remove(vs ...string) StringSet {
	for _, v := range vs {
		delete(*s, v)
	}
	return *s
}

func (s *StringSet) Sub(vs StringSet) StringSet {
	for v := range vs {
		delete(*s, v)
	}
	return *s
}

func (s StringSet) List() (vs []string) {
	count := len(s)
	if count == 0 {
		return
	}
	vs = make([]string, 0, count)
	for handle := range s {
		vs = append(vs, handle)
	}
	return
}

func (s StringSet) String() string {
	var (
		sb strings.Builder
		firstDone bool
	)
	sb.WriteByte('[')
	for str := range s {
		if firstDone {
			sb.WriteString(", ")
		} else {
			firstDone = true
		}
		sb.WriteByte('"')
		sb.WriteString(str)
		sb.WriteByte('"')
	}
	sb.WriteByte(']')
	return sb.String()
}
