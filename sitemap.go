// Package sitemap provides tools for creating, reading, and writing XML sitemaps
// and sitemap indexes according to the sitemaps.org protocol.
//
// This package supports:
//   - Creating standard XML sitemaps with URL entries
//   - Creating sitemap indexes for large sites
//   - Writing sitemaps to any io.Writer (including http.ResponseWriter)
//   - Reading and parsing existing sitemap XML files
//   - Minified output option for production use
//
// For detailed specifications on sitemap format and best practices,
// see https://www.sitemaps.org/
package sitemap

import (
	"encoding/xml"
	"io"
	"time"

	"github.com/snabb/diagio"
)

// ChangeFreq specifies how frequently a URL's content is likely to change.
// This is a hint to search engines and does not guarantee crawl frequency.
// Valid values are defined as constants below.
type ChangeFreq string

// Standard change frequency values as defined by sitemaps.org protocol.
// These constants should be used instead of raw strings for type safety.
const (
	Always  ChangeFreq = "always"  // Content changes with each access (e.g., real-time feeds)
	Hourly  ChangeFreq = "hourly"  // Content changes hourly
	Daily   ChangeFreq = "daily"   // Content changes daily
	Weekly  ChangeFreq = "weekly"  // Content changes weekly
	Monthly ChangeFreq = "monthly" // Content changes monthly
	Yearly  ChangeFreq = "yearly"  // Content changes yearly
	Never   ChangeFreq = "never"   // Content never changes (archived pages)
)

// URL represents a single URL entry in a sitemap or sitemap index.
//
// Fields:
//   - Loc: The fully qualified URL of the page (required)
//   - LastMod: The last modification time of the page (optional)
//   - ChangeFreq: How frequently the page content changes (optional)
//   - Priority: Priority of this URL relative to other URLs on your site (0.0-1.0, optional)
//
// Note: LastMod is a pointer to time.Time to enable proper XML omitempty behavior.
// When using URL in a SitemapIndex, ChangeFreq and Priority should be omitted
// as they are not valid for sitemap index entries.
type URL struct {
	Loc        string     `xml:"loc"`
	LastMod    *time.Time `xml:"lastmod,omitempty"`
	ChangeFreq ChangeFreq `xml:"changefreq,omitempty"`
	Priority   float32    `xml:"priority,omitempty"`
}

// Sitemap represents a complete XML sitemap containing multiple URL entries.
//
// The sitemap structure follows the sitemaps.org protocol specification.
// New instances should always be created using New() to ensure the correct
// XML namespace (xmlns) is set.
//
// Fields:
//   - SkipWriteHeader: If true, omits the XML declaration header when writing
//   - XMLName: Root element name ("urlset")
//   - Xmlns: XML namespace attribute (automatically set by New())
//   - URLs: Slice of URL entries in the sitemap
//   - Minify: If true, outputs compact XML without indentation (for production)
type Sitemap struct {
	SkipWriteHeader bool     `xml:"-"`
	XMLName         xml.Name `xml:"urlset"`
	Xmlns           string   `xml:"xmlns,attr"`

	URLs []*URL `xml:"url"`

	Minify bool `xml:"-"`
}

// New creates and initializes a new Sitemap instance with the correct
// XML namespace for the sitemaps.org protocol.
//
// Always use this function instead of directly creating a Sitemap struct
// to ensure proper namespace configuration.
//
// Example:
//
//	sm := sitemap.New()
//	sm.Add(&sitemap.URL{Loc: "https://example.com/page1"})
func New() *Sitemap {
	return &Sitemap{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  make([]*URL, 0),
	}
}

// Add appends a URL entry to the sitemap.
//
// The URL should have at minimum the Loc field populated with a valid URL.
// Other fields (LastMod, ChangeFreq, Priority) are optional.
func (s *Sitemap) Add(u *URL) {
	s.URLs = append(s.URLs, u)
}

// WriteTo serializes the sitemap to XML format and writes it to the provided io.Writer.
//
// This method implements the io.WriterTo interface, enabling efficient streaming
// of sitemap data. The output includes:
//   - XML declaration header (unless SkipWriteHeader is true)
//   - Pretty-printed XML with 2-space indentation (unless Minify is true)
//   - Trailing newline for POSIX compliance
//
// Parameters:
//   - w: The destination writer (e.g., file, http.ResponseWriter, buffer)
//
// Returns:
//   - n: Total number of bytes written
//   - err: Any error encountered during writing
//
// Example:
//
//	file, _ := os.Create("sitemap.xml")
//	defer file.Close()
//	sm.WriteTo(file)
func (s *Sitemap) WriteTo(w io.Writer) (n int64, err error) {
	cw := diagio.NewCounterWriter(w)

	if !s.SkipWriteHeader {
		_, err = cw.Write([]byte(xml.Header))
		if err != nil {
			return cw.Count(), err
		}
	}
	en := xml.NewEncoder(cw)
	if !s.Minify {
		en.Indent("", "  ")
	}
	err = en.Encode(s)
	if err != nil {
		return cw.Count(), err
	}
	_, err = cw.Write([]byte{'\n'})
	return cw.Count(), err
}

var _ io.WriterTo = (*Sitemap)(nil)

// ReadFrom parses an XML-encoded sitemap from the provided io.Reader and
// populates the Sitemap instance with the parsed data.
//
// This method implements the io.ReaderFrom interface, enabling efficient
// streaming parsing of sitemap XML files.
//
// Parameters:
//   - r: The source reader containing XML sitemap data
//
// Returns:
//   - n: Number of bytes read from the input
//   - err: Any error encountered during parsing
//
// Example:
//
//	file, _ := os.Open("sitemap.xml")
//	defer file.Close()
//	sm := sitemap.New()
//	sm.ReadFrom(file)
func (s *Sitemap) ReadFrom(r io.Reader) (n int64, err error) {
	de := xml.NewDecoder(r)
	err = de.Decode(s)
	return de.InputOffset(), err
}

var _ io.ReaderFrom = (*Sitemap)(nil)
