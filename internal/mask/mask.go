// Package mask hides personal identifiers in a document's text before it
// goes to a classifier outside the server. Each one becomes a typed
// placeholder ("[pan]", "[aadhaar]", "[person:pranav]"), so the model still
// knows what kind of document it is without seeing the values.
//
// It's pattern-based: a strong reduction, not anonymisation. Names and
// addresses that aren't labelled ("Name:", "Address:") or listed as people
// get through, and so does the subject matter itself (a diagnosis).
package mask

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Person is someone whose name (and its variants) becomes a numbered
// placeholder, "[person:1]", so not even first names leave the server.
type Person struct {
	Name    string   `json:"name"`    // also their tag
	Aliases []string `json:"aliases"` // other spellings, full names
}

// Placeholder is how the i-th person (from 0) appears in masked text.
func Placeholder(i int) string { return "[person:" + strconv.Itoa(i+1) + "]" }

type Options struct {
	People []Person
	// Words are masked wherever they appear (a family surname, a street).
	Words []string
}

type rule struct {
	re *regexp.Regexp
	// replace decides the placeholder for a match ("" leaves it).
	replace func(match string) string
}

func fixed(p string) func(string) string { return func(string) string { return p } }

// states are the codes Indian vehicle plates and licences start with.
const states = `(?:AN|AP|AR|AS|BR|CG|CH|DD|DL|DN|GA|GJ|HP|HR|JH|JK|KA|KL|LA|LD|MH|ML|MN|MP|MZ|NL|OD|OR|PB|PY|RJ|SK|TN|TR|TS|UA|UK|UP|WB)`

var (
	email = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	// A labelled personal value: the rest of the line after the label.
	labelled = regexp.MustCompile(`(?im)\b(name|father'?s\s+name|mother'?s\s+name|husband'?s\s+name|spouse'?s?\s+name|guardian'?s?\s+name|patient(?:'s)?\s+name|insured\s+name|nominee|address|addr|date\s+of\s+birth|d\.?o\.?b\.?|s/o|d/o|w/o|c/o)(\s*[:\-–]\s*|\s+)([^\n]+)`)
	// Upper-case identifiers, matched after the text is upper-cased.
	gstin    = regexp.MustCompile(`\b[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][0-9A-Z]Z[0-9A-Z]\b`)
	pan      = regexp.MustCompile(`\b[A-Z]{5}[0-9]{4}[A-Z]\b`)
	dl       = regexp.MustCompile(`\b` + states + `[ \-]?[0-9]{2}[ \-]?(?:19|20)[0-9]{2}[ \-]?[0-9]{7}\b`)
	vehicle  = regexp.MustCompile(`\b(?:` + states + `[ \-]?[0-9]{1,2}[ \-]?[A-Z]{1,3}[ \-]?[0-9]{4}|[0-9]{2}[ \-]?BH[ \-]?[0-9]{4}[ \-]?[A-Z]{1,2})\b`)
	passport = regexp.MustCompile(`\b[A-Z][0-9]{7}\b`)
	// Digit groups: Aadhaar (12), VID and cards (13–19), phones, and any
	// other long number. Dates are kept (see isDate).
	digits = regexp.MustCompile(`(?:\+91[ \-]?)?\b[0-9](?:[0-9]|[ \-][0-9]){5,}\b`)
	date   = regexp.MustCompile(`^(?:[0-9]{1,2}[\-/. ][0-9]{1,2}[\-/. ][0-9]{2,4}|[0-9]{4}[\-/. ][0-9]{1,2}[\-/. ][0-9]{1,2}|(?:19|20)[0-9]{2} ?[\-/] ?(?:[0-9]{2}|(?:19|20)[0-9]{2}))$`)
	// Tokens that may be numbers with OCR's letter-for-digit mistakes.
	confusable = regexp.MustCompile(`\b[0-9OoIlSB]{6,}\b`)
)

// Text masks text.
func Text(text string, o Options) string {
	text = maskPeople(text, o.People)
	text = maskWords(text, o.Words)
	text = email.ReplaceAllString(text, "[email]")
	text = maskLabelled(text)
	// Identifiers as read, then again once OCR's misread digits are fixed.
	text = maskIDs(text)
	text = maskIDs(fixConfusables(text))
	// Dates step aside while long numbers are masked.
	var dates []string
	text = dateRe.ReplaceAllStringFunc(text, func(d string) string {
		dates = append(dates, d)
		return "\x00" + strconv.Itoa(len(dates)-1) + "\x00"
	})
	text = digits.ReplaceAllStringFunc(text, numberPlaceholder)
	return regexp.MustCompile("\x00([0-9]+)\x00").ReplaceAllStringFunc(text, func(m string) string {
		i, _ := strconv.Atoi(m[1 : len(m)-1])
		return dates[i]
	})
}

func maskIDs(text string) string {
	text = maskUpper(text, gstin, "[gstin]")
	text = maskUpper(text, pan, "[pan]")
	text = maskUpper(text, dl, "[dl-no]")
	text = maskUpper(text, vehicle, "[vehicle-no]")
	return maskUpper(text, passport, "[passport-no]")
}

// maskUpper matches identifiers case-insensitively (OCR mixes case) and
// replaces them in the original text.
func maskUpper(text string, re *regexp.Regexp, placeholder string) string {
	upper := strings.ToUpper(text)
	if len(upper) != len(text) { // case changed byte lengths: match as is
		return re.ReplaceAllString(text, placeholder)
	}
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(upper, -1) {
		b.WriteString(text[last:m[0]])
		b.WriteString(placeholder)
		last = m[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

func wordPattern(words []string) *regexp.Regexp {
	var parts []string
	for _, w := range words {
		fields := strings.Fields(w)
		if len(fields) == 0 {
			continue
		}
		for i, f := range fields {
			fields[i] = regexp.QuoteMeta(f)
		}
		parts = append(parts, strings.Join(fields, `\s+`))
	}
	if len(parts) == 0 {
		return nil
	}
	// Longest first, so "Pranav Shrivastava" wins over "Pranav".
	for i := range parts {
		for j := i + 1; j < len(parts); j++ {
			if len(parts[j]) > len(parts[i]) {
				parts[i], parts[j] = parts[j], parts[i]
			}
		}
	}
	return regexp.MustCompile(`(?i)(?:` + strings.Join(parts, "|") + `)`)
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// replaceWords replaces whole-word matches in one pass.
func replaceWords(text string, re *regexp.Regexp, with string) string {
	if re == nil {
		return text
	}
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(text, -1) {
		before, _ := utf8.DecodeLastRuneInString(text[:m[0]])
		after, _ := utf8.DecodeRuneInString(text[m[1]:])
		if (m[0] > 0 && isWordRune(before)) || (m[1] < len(text) && isWordRune(after)) {
			continue
		}
		b.WriteString(text[last:m[0]])
		b.WriteString(with)
		last = m[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

func maskPeople(text string, people []Person) string {
	// Everyone's names in one pass, longest first, so a full name wins over
	// someone else's first name.
	type name struct {
		text string
		ph   string
	}
	var names []name
	for i, p := range people {
		for _, n := range append([]string{p.Name}, p.Aliases...) {
			if strings.TrimSpace(n) != "" {
				names = append(names, name{n, Placeholder(i)})
			}
		}
	}
	slices.SortStableFunc(names, func(a, b name) int { return len(b.text) - len(a.text) })
	for _, n := range names {
		text = replaceWords(text, wordPattern([]string{n.text}), n.ph)
	}
	return text
}

func maskWords(text string, words []string) string {
	return replaceWords(text, wordPattern(words), "[masked]")
}

var placeholderRe = regexp.MustCompile(`\[[a-z\-]+(?::[a-z0-9\-]+)?\]`)

// dateRe finds dates, which are kept: the number rules run around them.
var dateRe = regexp.MustCompile(`\b(?:[0-9]{1,2}[\-/.][0-9]{1,2}[\-/.](?:19|20)[0-9]{2}|(?:19|20)[0-9]{2}[\-/.][0-9]{1,2}[\-/.][0-9]{1,2})\b`)

// maskLabelled replaces the value after a personal label. People already
// turned into placeholders are kept, so "Name: [person:pranav] Kumar"
// becomes "Name: [person:pranav] [name]".
func maskLabelled(text string) string {
	return labelled.ReplaceAllStringFunc(text, func(m string) string {
		sub := labelled.FindStringSubmatch(m)
		label, sep, value := sub[1], sub[2], sub[3]
		kind := "[name]"
		switch l := strings.ToLower(label); {
		case strings.HasPrefix(l, "addr"), l == "c/o":
			kind = "[address]"
		case strings.Contains(l, "birth"), strings.HasPrefix(l, "d.o"), strings.HasPrefix(l, "dob"):
			kind = "[dob]"
		}
		kept := placeholderRe.FindAllString(value, -1)
		rest := strings.TrimSpace(placeholderRe.ReplaceAllString(value, ""))
		if hasWordChars(rest) {
			kept = append(kept, kind)
		}
		if len(kept) == 0 {
			return m
		}
		return label + sep + strings.Join(kept, " ")
	})
}

func hasWordChars(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// fixConfusables turns OCR's letter-for-digit mistakes back into digits in
// tokens that are mostly digits ("5O1OO123" → "50100123"), so number
// patterns still match.
func fixConfusables(text string) string {
	return confusable.ReplaceAllStringFunc(text, func(tok string) string {
		n := 0
		for _, r := range tok {
			if r >= '0' && r <= '9' {
				n++
			}
		}
		if n*10 < len(tok)*6 { // under 60% digits: a word, leave it
			return tok
		}
		return strings.NewReplacer("O", "0", "o", "0", "I", "1", "l", "1", "S", "5", "B", "8").Replace(tok)
	})
}

func numberPlaceholder(m string) string {
	trimmed := strings.TrimSpace(m)
	if date.MatchString(trimmed) {
		return m
	}
	var d strings.Builder
	for _, r := range m {
		if r >= '0' && r <= '9' {
			d.WriteRune(r)
		}
	}
	ds := d.String()
	if strings.HasPrefix(trimmed, "+91") && len(ds) == 12 {
		ds = ds[2:]
	}
	switch {
	case len(ds) == 10 && ds[0] >= '6' && ds[0] <= '9':
		return "[phone]"
	case len(ds) == 12 && ds[0] >= '2' && verhoeff(ds):
		return "[aadhaar]"
	case len(ds) >= 13 && len(ds) <= 19 && luhn(ds):
		return "[card-no]"
	case len(ds) == 16:
		return "[aadhaar-vid]"
	case len(ds) >= 6:
		return "[number]"
	}
	return m
}

// luhn is the card number checksum.
func luhn(ds string) bool {
	sum := 0
	for i := range len(ds) {
		d := int(ds[len(ds)-1-i] - '0')
		if i%2 == 1 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

var (
	verhoeffD = [10][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 2, 3, 4, 0, 6, 7, 8, 9, 5}, {2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
		{3, 4, 0, 1, 2, 8, 9, 5, 6, 7}, {4, 0, 1, 2, 3, 9, 5, 6, 7, 8}, {5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
		{6, 5, 9, 8, 7, 1, 0, 4, 3, 2}, {7, 6, 5, 9, 8, 2, 1, 0, 4, 3}, {8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
		{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	}
	verhoeffP = [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 5, 7, 6, 2, 8, 3, 0, 9, 4}, {5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
		{8, 9, 1, 6, 0, 4, 3, 5, 2, 7}, {9, 4, 5, 3, 1, 2, 6, 8, 7, 0}, {4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5}, {7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
)

// verhoeff is Aadhaar's checksum.
func verhoeff(ds string) bool {
	c := 0
	for i := range len(ds) {
		c = verhoeffD[c][verhoeffP[i%8][int(ds[len(ds)-1-i]-'0')]]
	}
	return c == 0
}

// Unmask puts people's names back where a classifier used their
// placeholder (in a suggested title), and reports whether any other
// placeholder is left.
func Unmask(s string, people []Person) (string, bool) {
	for i, p := range people {
		s = strings.ReplaceAll(s, Placeholder(i), p.Name)
	}
	return s, placeholderRe.MatchString(s)
}
