package jobs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Canned payloads. Tests never touch the network: a parser that only works when
// a board is up is a parser nobody can verify.
const kalibrrFixture = `{
  "count": 2,
  "jobs": [
    {"id": 273049, "name": "Backend Engineer", "slug": "backend-engineer",
     "description": "<p>Kami mencari <b>backend engineer</b> dengan Go dan PostgreSQL.</p>",
     "qualifications": "Pengalaman 3 tahun, Kubernetes",
     "company_name": "PT Maju Jaya", "company": {"code": "pt-maju-jaya"},
     "activation_date": "2026-09-28T02:44:23+00:00",
     "is_work_from_home": false, "function": "Engineering", "tenure": "Full time",
     "google_location": {"address_components": {"city": "Jakarta", "region": "DKI Jakarta", "country": "Indonesia"}}},
    {"id": 0, "name": "", "slug": "kosong", "description": ""}
  ]
}`

const remotiveFixture = `{"jobs":[
  {"id": 2091144, "url": "https://remotive.com/remote-jobs/x", "title": "Go Engineer",
   "company_name": "Remote Co", "category": "Software Development",
   "tags": ["golang", "postgres"], "job_type": "full_time",
   "publication_date": "2026-09-21T12:55:11", "candidate_required_location": "Worldwide",
   "description": "<div>Build <strong>Go</strong> services</div>"},
  {"id": 0, "title": "", "url": ""}
]}`

const remoteokFixture = `[
  {"legal": "API Terms of Service: please link back"},
  {"slug": "go-engineer-acme", "id": "1137434", "date": "2026-09-26T16:00:26+00:00",
   "company": "Acme", "position": "Senior Go Engineer", "tags": ["go", "postgres"],
   "description": "<p>Remote role</p>", "location": "Remote", "url": "https://remoteok.com/x"}
]`

const arbeitnowFixture = `{"data":[
  {"slug": "backend-berlin-1", "company_name": "Berlin Tech", "title": "Backend Engineer",
   "description": "<p>Go and Kubernetes</p>", "remote": true, "url": "https://arbeitnow.com/x",
   "tags": ["go"], "job_types": ["full-time"], "location": "Berlin", "created_at": 1790000000}
]}`

func TestParseKalibrr(t *testing.T) {
	list, err := parseKalibrr([]byte(kalibrrFixture))
	if err != nil {
		t.Fatalf("parseKalibrr: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("jobs = %d, mau 1 (entri tanpa id/nama harus dibuang)", len(list))
	}
	j := list[0]
	if j.Title != "Backend Engineer" || j.Company != "PT Maju Jaya" {
		t.Fatalf("judul/perusahaan = %q / %q", j.Title, j.Company)
	}
	// The company code is what makes the link work; the short form renders a
	// "page not available" shell.
	if j.URL != "https://www.kalibrr.com/c/pt-maju-jaya/jobs/273049/backend-engineer" {
		t.Fatalf("url = %q", j.URL)
	}
	if !strings.Contains(j.Location, "Jakarta") || !strings.Contains(j.Location, "Indonesia") {
		t.Fatalf("lokasi = %q", j.Location)
	}
	if strings.Contains(j.Description, "<") {
		t.Fatalf("deskripsi masih berisi tag HTML: %q", j.Description)
	}
	if !strings.Contains(j.Description, "Kualifikasi") {
		t.Fatal("kualifikasi tidak ikut masuk deskripsi")
	}
	if j.PublishedAt.IsZero() {
		t.Fatal("tanggal terbit tidak terbaca")
	}
}

func TestParseRemotive(t *testing.T) {
	list, err := parseRemotive([]byte(remotiveFixture))
	if err != nil {
		t.Fatalf("parseRemotive: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("jobs = %d, mau 1", len(list))
	}
	if !list[0].Remote {
		t.Fatal("remotive selalu remote")
	}
	if !strings.Contains(strings.Join(list[0].Tags, ","), "golang") {
		t.Fatalf("tag = %v", list[0].Tags)
	}
}

func TestParseRemoteOKSkipsTheLegalNotice(t *testing.T) {
	list, err := parseRemoteOK([]byte(remoteokFixture))
	if err != nil {
		t.Fatalf("parseRemoteOK: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("jobs = %d, mau 1: elemen pertama adalah pemberitahuan hukum, bukan lowongan", len(list))
	}
	if list[0].Title != "Senior Go Engineer" {
		t.Fatalf("judul = %q", list[0].Title)
	}
}

func TestParseArbeitnow(t *testing.T) {
	list, err := parseArbeitnow([]byte(arbeitnowFixture))
	if err != nil {
		t.Fatalf("parseArbeitnow: %v", err)
	}
	if len(list) != 1 || list[0].Title != "Backend Engineer" {
		t.Fatalf("hasil = %+v", list)
	}
	if list[0].PublishedAt.IsZero() {
		t.Fatal("epoch seconds tidak dikonversi")
	}
	if !strings.Contains(strings.Join(list[0].Tags, ","), "go") {
		t.Fatalf("tag = %v", list[0].Tags)
	}
}

func TestKalibrrURLNeedsTheCompanyCode(t *testing.T) {
	cases := []struct {
		id               int
		slug, code, want string
	}{
		{270342, "backend-developer-2", "trimegah-securities", "https://www.kalibrr.com/c/trimegah-securities/jobs/270342/backend-developer-2"},
		{270342, "backend-developer-2", "", "https://www.kalibrr.com/job-board?text=backend-developer-2"},
		{270342, "", "vlink-inc", "https://www.kalibrr.com/c/vlink-inc/jobs/270342"},
	}
	for _, c := range cases {
		if got := kalibrrURL(c.id, c.slug, c.code); got != c.want {
			t.Fatalf("kalibrrURL(%d, %q, %q) = %q, mau %q", c.id, c.slug, c.code, got, c.want)
		}
	}
}

func TestStripHTML(t *testing.T) {
	got := stripHTML("<p>Go &amp; Postgres</p><br/>Remote<br /><ul><li>Kubernetes</li></ul>")
	if strings.Contains(got, "<") || strings.Contains(got, "&amp;") {
		t.Fatalf("sisa markup: %q", got)
	}
	for _, want := range []string{"Go & Postgres", "Remote", "Kubernetes"} {
		if !strings.Contains(got, want) {
			t.Fatalf("kehilangan %q dalam %q", want, got)
		}
	}
}

const cvFixture = `Budi Santoso, Backend Engineer.
Empat tahun membangun layanan Go untuk pembayaran, PostgreSQL dan Kubernetes di produksi.
Keahlian: Go, PostgreSQL, Kubernetes, gRPC, observability, Docker.`

func TestProfileKeepsShortTechTokensAndDropsStopwords(t *testing.T) {
	p := NewProfile(cvFixture)
	if p.Keywords["go"] == 0 {
		t.Fatal("token \"go\" hilang: nama bahasa ini dua huruf dan tetap penting")
	}
	if p.Keywords["dan"] != 0 || p.Keywords["untuk"] != 0 {
		t.Fatal("stopword ikut jadi kata kunci")
	}
	if p.Keywords["postgresql"] == 0 {
		t.Fatal("keahlian utama tidak masuk profil")
	}
}

func TestRelevanceWeighsTheTitleAboveTheBody(t *testing.T) {
	p := NewProfile(cvFixture)
	titleMatch := Job{Title: "Backend Engineer (Go)", Company: "A", URL: "u", Tags: []string{"go"}}
	bodyOnly := Job{Title: "Sales Executive", Company: "B", URL: "u",
		Description: strings.Repeat("postgresql kubernetes grpc ", 20)}
	if p.Relevance(titleMatch) <= p.Relevance(bodyOnly) {
		t.Fatalf("judul yang cocok (%v) tidak lebih tinggi dari deskripsi panjang (%v)",
			p.Relevance(titleMatch), p.Relevance(bodyOnly))
	}
	if p.Relevance(Job{Title: "Sales Executive", URL: "u"}) != 0 {
		t.Fatal("lowongan tanpa kata kunci yang cocok seharusnya bernilai 0")
	}
}

func TestFilterAndRankDeduplicatesAndRespectsAge(t *testing.T) {
	p := NewProfile(cvFixture)
	fresh := time.Now().AddDate(0, 0, -3)
	old := time.Now().AddDate(0, 0, -400)
	in := []Job{
		{Source: "a", ExternalID: "1", Title: "Backend Engineer", Company: "Acme", URL: "u1", PublishedAt: fresh},
		// same posting reposted on another board
		{Source: "b", ExternalID: "2", Title: "backend  engineer", Company: "ACME", URL: "u2", PublishedAt: fresh},
		{Source: "a", ExternalID: "3", Title: "Backend Engineer", Company: "Old", URL: "u3", PublishedAt: old},
		{Source: "a", ExternalID: "4", Title: "Barista", Company: "Cafe", URL: "u4", PublishedAt: fresh},
		{Source: "a", ExternalID: "5", Title: "Backend Engineer", Company: "NoURL", PublishedAt: fresh},
	}
	got := FilterAndRank(p, in, 10, 75, "")
	if len(got) != 1 {
		t.Fatalf("kandidat = %d, mau 1 (duplikat, terlalu tua, tidak relevan, dan tanpa url harus jatuh): %+v", len(got), got)
	}
	if got[0].Company != "Acme" {
		t.Fatalf("yang tersisa = %+v", got[0])
	}
}

func TestFilterAndRankKeepsTheBestAndCapsTheList(t *testing.T) {
	p := NewProfile(cvFixture)
	now := time.Now()
	var in []Job
	for i := 0; i < 20; i++ {
		in = append(in, Job{
			Source: "s", ExternalID: string(rune('a' + i)),
			// A distinct company per posting: identical title+company is the
			// dedupe key, and merging them is the intended behaviour.
			Title: "Backend Engineer Go", Company: fmt.Sprintf("Co %d", i), URL: "u", PublishedAt: now,
		})
	}
	in = append(in, Job{Source: "s", ExternalID: "best", Title: "Kubernetes PostgreSQL Go Backend Engineer",
		Company: "Tepat", URL: "u", PublishedAt: now})
	got := FilterAndRank(p, in, 5, 75, "")
	if len(got) != 5 {
		t.Fatalf("hasil = %d, mau 5", len(got))
	}
	if got[0].ExternalID != "best" {
		t.Fatalf("yang paling relevan tidak di urutan pertama: %+v", got[0])
	}
}

func TestOpenToIndonesia(t *testing.T) {
	cases := map[string]bool{
		"Worldwide":                       true,
		"":                                true,
		"Anywhere":                        true,
		"Asia, APAC":                      true,
		"Indonesia":                       true,
		"Remote":                          true,
		"USA":                             false,
		"Remote - US":                     false,
		"Northern America, LATAM, Europe": false,
		"Europe":                          false,
		"USA, Canada, USA timezones":      false,
		"Philippines":                     false,
	}
	for location, want := range cases {
		if got := OpenToIndonesia(location); got != want {
			t.Fatalf("OpenToIndonesia(%q) = %v, mau %v", location, got, want)
		}
	}
}

func TestSelectFallsBackToDefaultsOnUnknownNames(t *testing.T) {
	got := Select([]string{"kalibrr", "tidak-ada-board-ini"})
	if len(got) != 1 || got[0].Name() != "kalibrr" {
		t.Fatalf("Select = %+v", got)
	}
	if names := Select(nil); len(names) != len(DefaultSourceNames()) {
		t.Fatalf("nama kosong tidak kembali ke bawaan: %+v", names)
	}
	// The default set is the Indonesian one: the local board plus Remotive.
	names := map[string]bool{}
	for _, s := range Select(DefaultSourceNames()) {
		names[s.Name()] = true
	}
	if !names["kalibrr"] || !names["remotive"] {
		t.Fatalf("sumber bawaan tidak memuat board lokal: %+v", names)
	}
}

func TestLocalFirstDeploymentDropsPostingsAbroad(t *testing.T) {
	p := NewProfile(cvFixture)
	now := time.Now()
	jobs := []Job{
		{Source: "s", ExternalID: "jakarta", Title: "Backend Engineer Go", Company: "A", URL: "u",
			Location: "South Jakarta, DKI Jakarta, Indonesia", PublishedAt: now},
		{Source: "s", ExternalID: "berlin", Title: "Backend Engineer Go PostgreSQL Kubernetes",
			Company: "B", URL: "u", Location: "Berlin, Germany", PublishedAt: now},
		{Source: "s", ExternalID: "unknown", Title: "Backend Engineer Go", Company: "C", URL: "u",
			PublishedAt: now},
		{Source: "s", ExternalID: "remote-world", Title: "Backend Engineer Go", Company: "D", URL: "u",
			Location: "Worldwide", Remote: true, PublishedAt: now},
		{Source: "s", ExternalID: "remote-us", Title: "Backend Engineer Go", Company: "E", URL: "u",
			Location: "Remote - US", Remote: true, PublishedAt: now},
		{Source: "s", ExternalID: "remote-unsaid", Title: "Backend Engineer Go", Company: "F", URL: "u",
			Location: "Remote", Remote: true, PublishedAt: now},
	}
	got := FilterAndRank(p, jobs, 10, 75, "Indonesia")
	keep := map[string]bool{}
	for _, j := range got {
		keep[j.ExternalID] = true
	}
	for _, want := range []string{"jakarta", "remote-world", "remote-unsaid"} {
		if !keep[want] {
			t.Fatalf("lowongan %q hilang padahal bisa diambil: %+v", want, keep)
		}
	}
	for _, bad := range []string{"berlin", "unknown", "remote-us"} {
		if keep[bad] {
			t.Fatalf("lowongan %q lolos padahal pelamar di Jakarta tidak bisa mengambilnya: %+v", bad, keep)
		}
	}
	// A local posting must beat a worldwide remote one when the keywords tie.
	if got[0].ExternalID != "jakarta" {
		t.Fatalf("yang lokal tidak di urutan pertama: %+v", got[0])
	}
}

func TestRankingPrefersWhatTheCandidateCanReach(t *testing.T) {
	p := NewProfile(cvFixture)
	now := time.Now()
	berlin := Job{Source: "s", ExternalID: "berlin", Title: "Backend Engineer (Go) PostgreSQL Kubernetes",
		Company: "Berlin Co", URL: "u", Location: "Berlin, Germany", PublishedAt: now}
	jakarta := Job{Source: "s", ExternalID: "jakarta", Title: "Backend Engineer Go",
		Company: "Jakarta Co", URL: "u", Location: "Jakarta, Indonesia", PublishedAt: now}
	remote := Job{Source: "s", ExternalID: "remote", Title: "Backend Engineer Go",
		Company: "Remote Co", URL: "u", Location: "Remote", Remote: true, PublishedAt: now}

	// The German posting matches the keywords best, and still must not win.
	got := FilterAndRank(p, []Job{berlin, jakarta, remote}, 2, 75, "Indonesia")
	for _, j := range got {
		if j.ExternalID == "berlin" {
			t.Fatalf("lowongan on-site luar negeri menang padahal ada yang bisa diambil: %+v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("hasil = %d, mau 2", len(got))
	}

	// Asking for a country explicitly flips the preference.
	got = FilterAndRank(p, []Job{berlin, jakarta}, 1, 75, "Germany")
	if got[0].ExternalID != "berlin" {
		t.Fatalf("preferensi negara tidak dihormati: %+v", got)
	}
}

func TestDefaultSourcesHaveUniqueNames(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Sources() {
		if seen[s.Name()] {
			t.Fatalf("nama sumber ganda: %s", s.Name())
		}
		seen[s.Name()] = true
	}
	for _, want := range []string{"kalibrr", "remotive", "remoteok", "arbeitnow"} {
		if !seen[want] {
			t.Fatalf("sumber %q tidak terdaftar", want)
		}
	}
	if !seen["kalibrr"] {
		t.Fatal("board lokal (kalibrr) hilang")
	}
}

// TestSourcesRespectCancelledContext documents that a slow board cannot hold a
// search hostage: every fetch takes a context and must give up when it is done.
func TestSourcesRespectCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, s := range Sources() {
		if _, err := s.Fetch(ctx, Query{Limit: 1}); err == nil {
			t.Fatalf("sumber %s mengabaikan context yang dibatalkan", s.Name())
		}
	}
}
