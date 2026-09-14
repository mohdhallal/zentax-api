package parsing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// These are the tests for reading a file in the encoding it was actually
// written in — or saying that it cannot be told, and refusing.
//
// The bytes below are written as escapes on purpose. A legacy codepage is not
// something a Go source file can hold as text: the whole point of these files
// is that the same byte is a different letter depending on a machine nobody
// asked about, so each one is spelled out as the bytes it is, with the name it
// was typed as in the comment beside it.

// readEncoded is the reading half over bytes built in the test rather than a
// fixture file, which is the only way to hold a file in an encoding this
// package refuses — and it hands back the error rather than failing on it,
// because the error IS what most of these tests are about.
func readEncoded(t *testing.T, name string, content []byte) (*domain.ReadOutcome, error) {
	t.Helper()
	return New().Read(context.Background(), domain.Upload{
		Target: domain.TargetEntities, FileName: name, Content: content,
	})
}

// A register in a legacy codepage used to be decoded as Windows-1252 and
// imported. Not refused, not warned about by cell — imported, with every
// accented name spelled in different letters and one file-level remark naming a
// codepage nobody had established.
//
// `Łódź Spółka z o.o.` came back as `£ódŸ Spó³ka z o.o.` and committed as a
// CREATE under that name. The name is the natural key this import matches on,
// so it was a permanent record that could never be matched to the customer's
// own register again — the exact outcome this module exists to prevent.
//
// Nothing in any of these files says which codepage it is. So none of them is
// read: the refusal names the cells, shows the bytes, and asks for the file as
// CSV UTF-8, which is the one thing that settles the question.
func TestRead_ALegacyCodepageIsRefusedRatherThanGuessed(t *testing.T) {
	t.Parallel()
	for _, file := range []struct {
		name    string
		content []byte
		cellRef string
		bytes   string
		// wouldHaveRead is what the old Windows-1252 fallback made of the same
		// bytes, asserted absent: a guess that is merely hedged is still a
		// value quietly changed.
		wouldHaveRead string
	}{
		{
			name: "a Polish register (Windows-1250): `Łódź Spółka z o.o.`",
			content: []byte("Entity Name,Country\n" +
				"\xA3\xF3d\x9F Sp\xF3\xB3ka z o.o.,Poland\n"),
			cellRef:       "A2",
			bytes:         "<0xA3>",
			wouldHaveRead: "£ódŸ Spó³ka z o.o.",
		},
		{
			name: "a Czech register (Windows-1250): `Česká Těžební a.s.`",
			content: []byte("Entity Name,Country\n" +
				"\xC8esk\xE1 T\xEC\x9Eebn\xED a.s.,Czechia\n"),
			cellRef:       "A2",
			bytes:         "<0xC8>",
			wouldHaveRead: "Èeská Tìžební a.s.",
		},
		{
			name: "a Russian register (Windows-1251): `ООО Ромашка`",
			content: []byte("Entity Name,Country\n" +
				"\xCE\xCE\xCE \xD0\xEE\xEC\xE0\xF8\xEA\xE0,Russia\n"),
			cellRef: "A2",
			bytes:   "<0xCE>",
			// All letters, no punctuation: the shape that no "does this look
			// like mojibake?" heuristic can catch, and the reason this refusal
			// is about evidence rather than about how the decoded text looks.
			wouldHaveRead: "ÎÎÎ Ðîìàøêà",
		},
		{
			name: "a byte Windows-1252 does not define at all (Windows-1250): `Ťatra Źródło sp.`",
			content: []byte("Entity Name,Country\n" +
				"\x8Datra \x8Fr\xF3d\xB3o sp.,Slovakia\n"),
			cellRef: "A2",
			bytes:   "<0x8D>",
			// The old decoder mapped the five bytes 1252 leaves undefined to
			// U+FFFD and stored the result as an entity name.
			wouldHaveRead: "�",
		},
		{
			name: "a German register (Windows-1252): `Müller GmbH`",
			content: []byte("Entity Name,Country\n" +
				"M\xFCller GmbH,Germany\n"),
			cellRef: "A2",
			bytes:   "<0xFC>",
			// This one the old fallback got RIGHT, and it is refused all the
			// same: the bytes cannot say so, and a reader that is right by luck
			// on this file is the same reader that was wrong on the four above.
			// Refusing costs this customer one Save As; guessing cost the other
			// four their entity names.
			wouldHaveRead: "Müller GmbH",
		},
	} {
		outcome, err := readEncoded(t, "register.csv", file.content)

		require.Error(t, err, file.name)
		assert.Nil(t, outcome, file.name, "nothing is read from a file whose encoding is a guess")

		message := err.Error()
		assert.Contains(t, message, "not UTF-8", file.name)
		assert.Contains(t, message, "nothing in it says what it is", file.name)
		assert.Contains(t, message, file.cellRef, file.name, "the cell the customer can go and look at")
		assert.Contains(t, message, file.bytes, file.name, "the byte itself, not a letter it might be")
		assert.Contains(t, message, "CSV UTF-8", file.name, "what it needs in order to read the file")
		assert.NotContains(t, message, file.wouldHaveRead, file.name,
			"the guess is not made, so it is not shown either")
	}
}

// The same register written the way the refusal asks for it reads, and reads as
// exactly what was typed. A refusal that cannot be satisfied is not a remedy.
func TestRead_TheSameRegisterInUTF8ReadsAsWritten(t *testing.T) {
	t.Parallel()
	outcome, err := readEncoded(t, "register.csv", []byte(
		"Entity Name,Country\n"+
			"Łódź Spółka z o.o.,Poland\n"+
			"Česká Těžební a.s.,Czechia\n"+
			"ООО Ромашка,Russia\n"+
			"Müller GmbH,Germany\n"))

	require.NoError(t, err)
	require.Len(t, outcome.Rows, 4)
	names := make([]string, 0, len(outcome.Rows))
	for _, row := range outcome.Rows {
		assert.Empty(t, errorsIn(row), "row %d", row.Number)
		names = append(names, row.Entity.Name)
	}
	assert.Equal(t, []string{
		"Łódź Spółka z o.o.", "Česká Těžební a.s.", "ООО Ромашка", "Müller GmbH",
	}, names)
	assert.NotContains(t, fileMessages(outcome), "not UTF-8")
}

// One byte that is not UTF-8 used to re-read the WHOLE file as Windows-1252 —
// every correctly written name in it changed to fix one cell, under a single
// remark ("this file is not UTF-8; it was read as Windows-1252") that was untrue
// of every byte in the file but one. `Müller GmbH` became `MÃ¼ller GmbH` and
// committed under that name.
//
// The file says otherwise, loudly: its other accented names are multi-byte UTF-8
// sequences that no single-byte codepage could have produced. So the reading
// stands and the evidence is bounded to the cell it is in — which is what a
// concatenated export, a value pasted out of an older system, or a file touched
// by an editor that knows nothing about encodings actually produces.
func TestRead_OneByteThatIsNotUTF8DoesNotRereadTheWholeFile(t *testing.T) {
	t.Parallel()
	outcome, err := readEncoded(t, "register.csv", []byte(
		"Entity Name,Country,Note\n"+
			"Müller GmbH,Germany,fine\n"+
			"Société Générale SA,France,r\xE9vision\n"))

	require.Error(t, err)
	assert.Nil(t, outcome)

	message := err.Error()
	assert.Contains(t, message, "UTF-8 apart from 1 byte that is not",
		"one byte is one byte, not a verdict on the file")
	assert.Contains(t, message, `C3 holds "r<0xE9>vision"`, "the cell it is actually in")
	assert.NotContains(t, message, "A2", "a cell that is perfectly good UTF-8 is not named")
	assert.NotContains(t, message, "Windows-1252", "no codepage is named, because none was established")
	for _, mojibake := range []string{"MÃ¼ller", "SociÃ©tÃ©", "GÃ©nÃ©rale"} {
		assert.NotContains(t, message, mojibake,
			"the names that were written correctly are not rewritten to accommodate one byte")
	}
}

// The stray byte in a column the report itself says is not imported is still
// the file's problem, and this is the shape the whole-file re-read was worst
// for: the customer is told column C will not be imported, and every value in
// the columns that ARE imported comes back spelled differently because of it.
func TestRead_AStrayByteInAnUnimportedColumnIsNamedNotSpread(t *testing.T) {
	t.Parallel()
	_, err := readEncoded(t, "register.csv", []byte(
		"Entity Name,Country,Cost centre\n"+
			"Acme Holding AG,Switzerland,Z\xFCrich\n"+
			"Acme Süd GmbH,Germany,Munich\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), `C2 holds "Z<0xFC>rich"`)
	assert.NotContains(t, err.Error(), "SÃ¼d", "the name in the column that IS imported is left alone")
}

// The cell a refusal names has to be the cell the byte is in, in the shapes a
// real export actually has: another separator, a quoted value with a line break
// inside it, and a table that does not start on row 1. A refusal that sends
// somebody to the wrong cell is worse than one that names none.
func TestRead_TheCellNamedIsTheCellTheByteIsIn(t *testing.T) {
	t.Parallel()
	_, err := readEncoded(t, "register.csv", []byte(
		"Konzern-Register;;\n"+
			"\n"+
			"Entity Name;Country;Registered office\n"+
			"Acme Holding AG;Switzerland;\"Bahnhofstrasse 1\nZ\xFCrich 8001\"\n"+
			"Acme Süd GmbH;Germany;Munich\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), `C4 holds "Bahnhofstrasse 1`,
		"the column the byte is in, and the row that record starts on")
	assert.Contains(t, err.Error(), `Z<0xFC>rich 8001"`, "the line break inside the cell does not move it")
}

// A byte-order mark is a declaration, not a proof. A file two exports were
// concatenated into carries the first one's mark and the second one's bytes,
// and the mark used to be taken at its word — the bytes after it went through
// untouched and became REPLACEMENT CHARACTERS inside values much later, where
// nothing could tell them from a character somebody had typed.
func TestRead_AByteOrderMarkIsNotProofOfWhatFollowsIt(t *testing.T) {
	t.Parallel()
	_, err := readEncoded(t, "register.csv", append([]byte{0xEF, 0xBB, 0xBF},
		[]byte("Entity Name,Country\nM\xFCller GmbH,Germany\n")...))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not UTF-8")
	assert.Contains(t, err.Error(), `A2 holds "M<0xFC>ller GmbH"`,
		"the mark is three bytes, and the cell reference counts from the table, not from them")
}

// The refusal names several cells rather than one, because one cell is a value
// somebody pasted and a file full of them is the file's encoding. It names a
// few and counts the bytes: a message listing every cell in a thousand-row
// register is a message nobody reads.
func TestRead_AnEncodingRefusalNamesEnoughCellsToShowTheShape(t *testing.T) {
	t.Parallel()
	_, err := readEncoded(t, "register.csv", []byte(
		"Entity Name,Country\n"+
			"Zentral \xD6sterreich GmbH,Austria\n"+
			"Zentral Espa\xF1a SL,Spain\n"+
			"Zentral M\xFCnchen GmbH,Germany\n"+
			"Zentral Lyon S\xC0RL,France\n"))

	require.Error(t, err)
	message := err.Error()
	assert.Contains(t, message, "4 of its bytes are not valid UTF-8")
	assert.Contains(t, message, "for example", "the cells named are a sample, and say so")
	assert.Contains(t, message, "A2 holds")
	assert.Contains(t, message, "A3 holds")
	assert.Contains(t, message, "A4 holds")
	assert.NotContains(t, message, "A5 holds", "three is the sample; the count carries the rest")
}

// UTF-16 is a different question with a different answer, and it must stay that
// way. Its byte pattern is proof rather than a guess — a NUL is not a character
// anybody types into a spreadsheet — so a "Unicode text" export is read, and the
// reading is recorded. Refusing everything that is not UTF-8 would take this
// file down with the codepages, and it does not belong there.
func TestRead_UTF16IsStillReadBecauseItsBytesSaySo(t *testing.T) {
	t.Parallel()
	text := "Entity Name\tCountry\nZentral Österreich GmbH\tAustria\n"
	content := []byte{0xFF, 0xFE}
	for _, r := range text {
		content = append(content, byte(r), byte(r>>8))
	}

	outcome, err := readEncoded(t, "unicode.txt", content)
	require.NoError(t, err)
	require.Len(t, outcome.Rows, 1)
	assert.Equal(t, "Zentral Österreich GmbH", outcome.Rows[0].Entity.Name)
	assert.Contains(t, fileMessages(outcome), "UTF-16 (little-endian)")
}
