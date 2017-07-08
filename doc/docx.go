package doc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
)

const (
	// xmlFileName is the one true XML file in a docx file that has
	// the textual information we desire
	xmlFileName = "word/document.xml"
)

var (
	// ErrMissingDocument happens when xmlFileName is missing from zip
	ErrMissingDocument = fmt.Errorf("Unable to find %s in docx", xmlFileName)
)

// Docx parses docx-formated readers
// this is go routine safe
type Docx struct {
	xmlData []byte
	Image   []byte
}

// NewDocx creates a new Docx instance with data from the given reader
func NewDocx(reader io.ReaderAt, size int64) (doc *Docx, err error) {
	doc = new(Docx)

	// docx files are just zip'd xml documents
	zipReader, err := zip.NewReader(reader, size)
	if err != nil {
		return
	}

	// find the xmlFileName file in the zip
	var fileReader io.ReadCloser
	for _, file := range zipReader.File {
		if doc.xmlData != nil && doc.Image != nil {
			return
		}

		lowerFileName := strings.ToLower(file.Name)
		if doc.Image == nil && (strings.HasSuffix(lowerFileName, ".jpg") || strings.HasSuffix(lowerFileName, ".jpeg")) {
			fileReader, err = file.Open()
			if err != nil {
				continue
			}

			defer fileReader.Close()

			doc.Image, err = ioutil.ReadAll(fileReader)
			if err != nil {
				return
			}
		} else if doc.xmlData == nil && lowerFileName == xmlFileName {
			// open xmlFileName for extraction
			fileReader, err = file.Open()
			if err != nil {
				return
			}

			defer fileReader.Close()

			// store all extracted XML data to doc.xmlData
			doc.xmlData, err = ioutil.ReadAll(fileReader)
			if err != nil {
				return
			}
		}
	}

	if doc.xmlData != nil && doc.Image != nil {
		return
	}

	return nil, ErrMissingDocument
}

// Text returns each line of (unformatted) text from the docx xml
func (d *Docx) Text() (lines []string, err error) {
	// create an XML decoder for the raw xml data
	decoder := xml.NewDecoder(bytes.NewReader(d.xmlData))

	// determines if xml.CharData tokens should start to be added to the
	// lines slice
	outputCharData := false

	var token xml.Token
	for {
		// get the current xml token
		token, err = decoder.Token()
		if err != nil {
			// end of file reached, reset err to nil
			if err == io.EOF {
				err = nil
			}

			return
		}

		switch t := token.(type) {
		case xml.StartElement:
			// only start outputing chardata xml tokens if we started to look at
			// the "body" of the xml document
			if !outputCharData && strings.ToLower(t.Name.Local) == "body" {
				outputCharData = true
			}

			break

		case xml.CharData:
			if outputCharData {
				// cast to string and get rid of unneeded whitespace
				str := strings.TrimSpace(string(t))

				// only add lines that actually have data
				if str != "" {
					lines = append(lines, str)
				}
			}

			break
		}
	}
}
