package kourai

import (
	"io/fs"
	"regexp"
	"strings"
	"time"
)

type fileMTimeFilter struct {
	after  *time.Time
	before *time.Time
}

func (f fileMTimeFilter) exclude(info fs.FileInfo) bool {
	var a bool
	var b bool
	if f.after != nil {
		a = !info.ModTime().After(*f.after)
	}
	if f.before != nil {
		b = !info.ModTime().Before(*f.before)
	}
	return a || b
}

type fileExtensionFilter struct {
	extensions map[string]bool
}

// files whose extensions are not contained in the fileExtensionFilter
// are excluded
func (f fileExtensionFilter) exclude(info fs.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	split := strings.Split(info.Name(), ".")
	ext := strings.ToLower(split[len(split)-1])
	_, ok := f.extensions[ext]
	return !ok
}

func newFileExtensionFilter(extensions []string) fileExtensionFilter {
	f := fileExtensionFilter{map[string]bool{}}
	for _, ext := range extensions {
		f.extensions[strings.ToLower(ext)] = true
	}
	return f
}

type RegexpFilter struct {
	excludes []*regexp.Regexp
}

func (f RegexpFilter) exclude(info fs.FileInfo) bool {
	for _, e := range f.excludes {
		if e.MatchString(info.Name()) {
			//fmt.Println("skipping", info.Name(), "which matched", e)
			return true
		}
	}
	return false
}

func NewRegexpFilter(excludes []string) RegexpFilter {
	f := RegexpFilter{}
	for _, p := range excludes {
		f.excludes = append(f.excludes, regexp.MustCompile(p))
	}
	return f
}

type fileFilter interface {
	exclude(fs.FileInfo) bool
}

type countryFilter struct {
	countries map[string]bool
}

func (f countryFilter) exclude(m Media) bool {
	for _, country := range m.Countries {
		if _, ok := f.countries[country]; ok {
			return true
		}
	}
	return false
}

type mediaFilter interface {
	exclude(Media) bool
}
