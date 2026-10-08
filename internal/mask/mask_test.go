package mask

import (
	"strings"
	"testing"
)

// aadhaarLike makes a made-up 12-digit number with a valid Verhoeff check
// digit.
func aadhaarLike(first11 string) string {
	inv := [10]int{0, 4, 3, 2, 1, 5, 6, 7, 8, 9}
	c := 0
	for i := range len(first11) {
		c = verhoeffD[c][verhoeffP[(i+1)%8][int(first11[len(first11)-1-i]-'0')]]
	}
	return first11 + string(rune('0'+inv[c]))
}

var people = []Person{
	{Name: "Pranav", Aliases: []string{"Pranav Kumar Sample", "P. K. Sample"}},
	{Name: "Sushma"},
}

func TestMask(t *testing.T) {
	a := aadhaarLike("23456789012")
	if !verhoeff(a) {
		t.Fatal("bad fixture")
	}
	in := strings.Join([]string{
		"GOVERNMENT OF INDIA",
		"Pranav Kumar Sample",
		"Aadhaar no " + a[:4] + " " + a[4:8] + " " + a[8:],
		"VID: 9134 5678 2345 6789",
		"Permanent Account Number ABCPS1234K",
		"Passport No. Z1234567 valid till 03/12/2031",
		"DL No: MH01 20190001234",
		"Vehicle: MH 01 AB 1234",
		"Card 4111 1111 1111 1111",
		"Mobile +91 98765 43210, email p.sample@example.com",
		"A/c No: 5O1OO123456789",
		"Consumer no 001234567",
		"Father's Name: Ramesh Sample",
		"S/O Sushma Devi",
		"Address: 221 Sample Street, Ward 14",
		"Date of Birth: 01-01-1960",
		"Policy period 20 Nov 2025 to 19 Nov 2026, premium Rs 12,345.00",
		"Assessment year 2026-27 FY 2025-2026 Form No. 16",
		"GSTIN 27ABCPS1234K1Z5",
		"Patient: P. K. Sample, Haemoglobin 13.8 g/dL",
	}, "\n")
	out := Text(in, Options{People: people, Words: []string{"Sample Street"}})
	t.Log("\n" + out)

	for _, leak := range []string{a[:4] + " " + a[4:8], "9134 5678", "ABCPS1234K", "Z1234567", "20190001234",
		"MH 01 AB 1234", "4111", "001234567", "98765", "p.sample@", "123456789", "Ramesh", "Devi", "Sample Street", "01-01-1960",
		"Pranav", "Sushma", "P. K."} {
		if strings.Contains(out, leak) {
			t.Errorf("%q leaked", leak)
		}
	}
	for _, want := range []string{"[aadhaar]", "[aadhaar-vid]", "[pan]", "[passport-no]", "[dl-no]", "[vehicle-no]",
		"[card-no]", "A/c No: [", "[phone]", "[email]", "[number]", "Father's Name: [name]", "S/O [person:2] [name]",
		"Address: [masked] [address]", "Date of Birth: [dob]", "[person:1]", "[gstin]",
		// kept: what classifying needs
		"GOVERNMENT OF INDIA", "03/12/2031", "20 Nov 2025 to 19 Nov 2026", "Rs 12,345.00", "2026-27", "2025-2026",
		"Form No. 16", "Haemoglobin 13.8 g/dL"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestChecksums(t *testing.T) {
	if !luhn("4111111111111111") || luhn("4111111111111112") {
		t.Fatal("luhn")
	}
	a := aadhaarLike("98765432109")
	if !verhoeff(a) || verhoeff(a[:11]+string(rune('0'+(int(a[11]-'0')+1)%10))) {
		t.Fatal("verhoeff")
	}
	// A 12-digit number failing the checksum is still masked, just as a number.
	if got := Text("Ref 234567890124", Options{}); got != "Ref [number]" && got != "Ref [aadhaar]" {
		t.Fatalf("got %q", got)
	}
}

func TestDatesKept(t *testing.T) {
	in := "CamScanner 03-15-2021 10.22 issued 2024-01-09 and 5.6.2020"
	if got := Text(in, Options{}); got != in {
		t.Fatalf("got %q", got)
	}
}

func TestWordsKept(t *testing.T) {
	// Ordinary words made of confusable letters, and short numbers, stay.
	in := "SOIL BOSS Ward 14 House no 221 Page 3 of 12"
	if got := Text(in, Options{}); got != in {
		t.Fatalf("got %q", got)
	}
}

func TestUnmask(t *testing.T) {
	got, left := Unmask("Aadhaar card – [person:1]", people)
	if got != "Aadhaar card – Pranav" || left {
		t.Fatalf("%q %v", got, left)
	}
	if _, left := Unmask("Policy [number]", people); !left {
		t.Fatal("placeholder not reported")
	}
}

func TestTitle(t *testing.T) {
	o := Options{People: []Person{{Name: "Pranav", Aliases: []string{"Dad", "Papa"}}, {Name: "Vritika"}}, Words: []string{"Sample"}}
	for in, want := range map[string]string{
		"Address proof Pune":          "Address proof Pune",
		"Name change affidavit":       "Name change affidavit",
		"Nominee form LIC":            "Nominee form LIC",
		"DOB certificate Vritika":     "DOB certificate [person:2]",
		"Dad passport 2019":           "[person:1] passport 2019",
		"PAN ABCPS1234K Papa":         "PAN [pan] [person:1]",
		"Policy 448812 FY 2025-26":    "Policy [number] FY 2025-26",
		"Sample house deed":           "[masked] house deed",
		"CamScanner 03-15-2021 10.22": "CamScanner 03-15-2021 10.22",
	} {
		if got := Title(in, o); got != want {
			t.Errorf("Title(%q) = %q, want %q", in, got, want)
		}
	}
}
