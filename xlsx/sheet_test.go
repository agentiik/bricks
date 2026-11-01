package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestColumnsAreLettersBothWays(t *testing.T) {
	for n, s := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if letters(n) != s {
			t.Errorf("letters(%d) is %s, want %s", n, letters(n), s)
		}
		if got, err := column(s + "12"); err != nil || got != n {
			t.Errorf("column(%s12) is %d, %v, want %d", s, got, err, n)
		}
	}
}

// What is written is read back as written, each cell with its type, and writing the same rows
// twice writes the same bytes, since a table's digest is its item's identity.
func TestAWorkbookReadsBackAsWritten(t *testing.T) {
	rows := [][]cell{
		{"ref", "amount", "paid", "note"},
		{"a1", json.Number("1200.5"), true, nil},
		{"b2 & <c3>", 3400.0, false, map[string]any{"k": "v"}},
	}
	b, err := writeSheet("Orders", rows)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := writeSheet("Orders", rows)
	if !bytes.Equal(b, again) {
		t.Error("the same rows wrote two different files")
	}
	got, err := readSheet(b, "Orders")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]cell{
		{"ref", "amount", "paid", "note"},
		{"a1", json.Number("1200.5"), true, nil},
		{"b2 & <c3>", json.Number("3400"), false, `{"k":"v"}`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %#v\nwant %#v", got, want)
	}
	if _, err := readSheet(b, "Invoices"); err == nil {
		t.Error("a sheet the workbook does not have was read")
	}
}

// A workbook as a spreadsheet program writes one: text in a shared strings table, a row skipped,
// cells referenced by position with gaps between them.
func TestSharedStringsAndGapsAreRead(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"xl/workbook.xml":            `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="S" sheetId="1" r:id="rId7"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId7" Target="worksheets/data.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>ref</t></si><si><r><t>to</t></r><r><t>tal</t></r></si><si><t>a1</t></si></sst>`,
		"xl/worksheets/data.xml":     `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="s"><v>1</v></c></row><row r="3"><c r="A3" t="s"><v>2</v></c><c r="C3"><v>42</v></c></row></sheetData></worksheet>`,
	} {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	got, err := readSheet(buf.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]cell{{"ref", nil, "total"}, {nil, nil, nil}, {"a1", nil, json.Number("42")}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read %#v\nwant %#v", got, want)
	}
}
