package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// An xlsx file is a zip of XML documents, Office Open XML's SpreadsheetML. This reads and writes
// the part of it a table needs, with the standard library alone, so that the image stays one
// static binary on scratch: the workbook's sheets, each sheet's rows and cells, and the shared
// strings a writer may keep its text in. Formulas are read as the value the file cached for them,
// styles are ignored, and a date is a number, since nothing but a style says it is one.

// cell is one value of a row: a string, a number, a boolean, or nil for an empty cell.
type cell = any

// readSheet is the rows of one sheet, named or the first, every row as long as the longest.
func readSheet(content []byte, sheet string) ([][]cell, error) {
	z, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("not an xlsx file, which is a zip: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	var workbook struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := decode(files, "xl/workbook.xml", &workbook); err != nil {
		return nil, err
	}
	if len(workbook.Sheets) == 0 {
		return nil, fmt.Errorf("the workbook has no sheet")
	}
	chosen := workbook.Sheets[0]
	if sheet != "" {
		found := false
		var names []string
		for _, s := range workbook.Sheets {
			names = append(names, s.Name)
			if s.Name == sheet {
				chosen, found = s, true
			}
		}
		if !found {
			return nil, fmt.Errorf("the workbook has no sheet %q: it has %s", sheet, strings.Join(names, ", "))
		}
	}
	var rels struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := decode(files, "xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, err
	}
	target := ""
	for _, r := range rels.Rels {
		if r.ID == chosen.RID {
			target = r.Target
		}
	}
	if target == "" {
		return nil, fmt.Errorf("the sheet %q points at no part of the file", chosen.Name)
	}
	if strings.HasPrefix(target, "/") {
		target = strings.TrimPrefix(target, "/")
	} else {
		target = path.Join("xl", target)
	}

	var shared []string
	if _, ok := files["xl/sharedStrings.xml"]; ok {
		var sst struct {
			SI []struct {
				T string `xml:"t"`
				R []struct {
					T string `xml:"t"`
				} `xml:"r"`
			} `xml:"si"`
		}
		if err := decode(files, "xl/sharedStrings.xml", &sst); err != nil {
			return nil, err
		}
		for _, si := range sst.SI {
			// A string is its plain text, or the text of its runs where it carries formatting.
			s := si.T
			for _, r := range si.R {
				s += r.T
			}
			shared = append(shared, s)
		}
	}

	var ws struct {
		Rows []struct {
			R     int `xml:"r,attr"`
			Cells []struct {
				Ref string `xml:"r,attr"`
				T   string `xml:"t,attr"`
				V   string `xml:"v"`
				IS  struct {
					T string `xml:"t"`
					R []struct {
						T string `xml:"t"`
					} `xml:"r"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := decode(files, target, &ws); err != nil {
		return nil, err
	}
	var rows [][]cell
	width := 0
	for _, r := range ws.Rows {
		// A row with no number is the one after the last; an empty row skipped by the file is
		// an empty row of the table, so the table keeps the shape a person sees.
		at := r.R
		if at == 0 {
			at = len(rows) + 1
		}
		for len(rows) < at-1 {
			rows = append(rows, nil)
		}
		var row []cell
		for j, c := range r.Cells {
			col := j
			if c.Ref != "" {
				col, err = column(c.Ref)
				if err != nil {
					return nil, err
				}
			}
			for len(row) < col {
				row = append(row, nil)
			}
			var v cell
			switch c.T {
			case "s":
				n, err := strconv.Atoi(c.V)
				if err != nil || n < 0 || n >= len(shared) {
					return nil, fmt.Errorf("cell %s names shared string %q, and there are %d", c.Ref, c.V, len(shared))
				}
				v = shared[n]
			case "inlineStr":
				s := c.IS.T
				for _, r := range c.IS.R {
					s += r.T
				}
				v = s
			case "str", "e":
				v = c.V
			case "b":
				v = c.V == "1"
			default:
				if c.V == "" {
					v = nil
				} else if n, err := strconv.ParseFloat(c.V, 64); err == nil {
					v = json.Number(strconv.FormatFloat(n, 'f', -1, 64))
				} else {
					v = c.V
				}
			}
			row = append(row, v)
		}
		width = max(width, len(row))
		rows = append(rows, row)
	}
	for i := range rows {
		for len(rows[i]) < width {
			rows[i] = append(rows[i], nil)
		}
	}
	return rows, nil
}

func decode(files map[string]*zip.File, name string, v any) error {
	f, ok := files[name]
	if !ok {
		return fmt.Errorf("the file has no %s, which every workbook carries", name)
	}
	r, err := f.Open()
	if err != nil {
		return fmt.Errorf("%s cannot be read: %w", name, err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("%s cannot be read: %w", name, err)
	}
	if err := xml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s is not the XML it should be: %w", name, err)
	}
	return nil
}

// column is the index of a cell reference's column: A1 is 0, AA7 is 26.
func column(ref string) (int, error) {
	n := 0
	i := 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
	}
	if i == 0 {
		return 0, fmt.Errorf("cell reference %q names no column", ref)
	}
	return n - 1, nil
}

// letters is the column's name: 0 is A, 26 is AA.
func letters(n int) string {
	s := ""
	for n++; n > 0; n = (n - 1) / 26 {
		s = string(rune('A'+(n-1)%26)) + s
	}
	return s
}

// epoch is every part's modification time: two runs over one batch write the same bytes, so the
// table's digest, which is its item's identity, is the same too.
var epoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// writeSheet writes a workbook of one sheet holding rows. A string is written inline rather than
// in a shared strings table, which keeps the file to five parts; a number is a number, a boolean a
// boolean, and anything else its JSON, as text.
func writeSheet(sheet string, rows [][]cell) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var data strings.Builder
	for i, row := range rows {
		fmt.Fprintf(&data, `<row r="%d">`, i+1)
		for j, v := range row {
			ref := letters(j) + strconv.Itoa(i+1)
			switch v := v.(type) {
			case nil:
			case string:
				fmt.Fprintf(&data, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, esc(v))
			case bool:
				b := 0
				if v {
					b = 1
				}
				fmt.Fprintf(&data, `<c r="%s" t="b"><v>%d</v></c>`, ref, b)
			case float64:
				fmt.Fprintf(&data, `<c r="%s"><v>%s</v></c>`, ref, strconv.FormatFloat(v, 'f', -1, 64))
			case json.Number:
				fmt.Fprintf(&data, `<c r="%s"><v>%s</v></c>`, ref, v)
			default:
				j, err := json.Marshal(v)
				if err != nil {
					return nil, err
				}
				fmt.Fprintf(&data, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, esc(string(j)))
			}
		}
		data.WriteString(`</row>`)
	}
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="` + esc(sheet) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
		{"xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` + data.String() + `</sheetData></worksheet>`},
	}
	for _, p := range parts {
		w, err := z.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: epoch})
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, p.body); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// keys is every key of the items' data, sorted, which is a table's columns.
func keys(items []map[string]any) []string {
	seen := map[string]bool{}
	for _, d := range items {
		for k := range d {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
