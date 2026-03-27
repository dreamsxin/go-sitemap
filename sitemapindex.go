// Package sitemap provides sitemap index support for large websites.
//
// Sitemap indexes are used when a site has more than 50,000 URLs or exceeds
// 50MB in size. The index references multiple individual sitemap files.

package sitemap

import (
	"encoding/xml"
	"io"

	"github.com/snabb/diagio"
)

// SitemapIndex represents a sitemap index file that references multiple
// individual sitemap files.
//
// Use a sitemap index when:
//   - Your site has more than 50,000 URLs
//   - Your uncompressed sitemap exceeds 50MB
//   - You want to organize sitemaps by section or type
//
// Unlike regular sitemaps, index entries (URLs) should only contain:
//   - Loc: URL to the individual sitemap file
//   - LastMod: Optional last modification time
//
// ChangeFreq and Priority are NOT valid for sitemap index entries.
//
// Fields:
//   - XMLName: Root element name ("sitemapindex")
//   - Xmlns: XML namespace (automatically set by NewSitemapIndex())
//   - URLs: Slice of sitemap URL entries
//   - Minify: If true, outputs compact XML without indentation
type SitemapIndex struct {
	XMLName xml.Name `xml:"sitemapindex"`
	Xmlns   string   `xml:"xmlns,attr"`

	URLs []*URL `xml:"sitemap"`

	Minify          bool `xml:"-"`
	SkipWriteHeader bool `xml:"-"` // If true, omit the XML declaration header
}

// NewSitemapIndex creates and initializes a new SitemapIndex instance with
// the correct XML namespace for the sitemaps.org protocol.
//
// Always use this function instead of directly creating a SitemapIndex struct
// to ensure proper namespace configuration.
//
// Example:
//
//	idx := sitemap.NewSitemapIndex()
//	idx.Add(&sitemap.URL{Loc: "https://example.com/sitemap1.xml"})
//	idx.Add(&sitemap.URL{Loc: "https://example.com/sitemap2.xml"})
func NewSitemapIndex() *SitemapIndex {
	return &SitemapIndex{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  make([]*URL, 0),
	}
}

// Add appends a sitemap URL entry to the index.
//
// The URL's Loc field should point to a valid sitemap XML file location.
// LastMod can optionally be set to indicate when the referenced sitemap
// was last updated.
func (s *SitemapIndex) Add(u *URL) {
	s.URLs = append(s.URLs, u)
}

// WriteTo serializes the sitemap index to XML format and writes it to the
// provided io.Writer.
//
// This method implements the io.WriterTo interface for efficient streaming.
// Output includes XML declaration header, optional indentation, and trailing newline.
//
// Parameters:
//   - w: The destination writer (e.g., file, http.ResponseWriter)
//
// Returns:
//   - n: Total number of bytes written
//   - err: Any error encountered during writing
func (s *SitemapIndex) WriteTo(w io.Writer) (n int64, err error) {
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

var _ io.WriterTo = (*SitemapIndex)(nil)

// ReadFrom parses an XML-encoded sitemap index from the provided io.Reader
// and populates the SitemapIndex instance with the parsed data.
//
// This method implements the io.ReaderFrom interface for efficient streaming parsing.
//
// Parameters:
//   - r: The source reader containing XML sitemap index data
//
// Returns:
//   - n: Number of bytes read from the input
//   - err: Any error encountered during parsing
func (s *SitemapIndex) ReadFrom(r io.Reader) (n int64, err error) {
	de := xml.NewDecoder(r)
	err = de.Decode(s)
	return de.InputOffset(), err
}

var _ io.ReaderFrom = (*SitemapIndex)(nil)
