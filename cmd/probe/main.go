// Command probe shows what the matcher would see, without spending model calls
// and without touching the database.
//
// It exists because the two stages of /cari fail in different ways: a board can
// go quiet, an eligibility rule can drop too much, and the ranking can prefer the
// wrong kind of posting. All three are visible here in seconds and cost nothing,
// where the same information from a real run costs quota and a minute of model
// time.
//
//	go run ./cmd/probe -cv /tmp/cv.txt
//	go run ./cmd/probe -cv /tmp/cv.txt -sources kalibrr,remotive,remoteok -keep 15
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gaisuke/profx/internal/jobs"
)

func main() {
	cvPath := flag.String("cv", "", "file berisi teks CV (wajib)")
	sourceNames := flag.String("sources", strings.Join(jobs.DefaultSourceNames(), ","), "sumber dipisah koma")
	keep := flag.Int("keep", 10, "jumlah kandidat yang ditampilkan")
	country := flag.String("country", "Indonesia", "negara yang dianggap terjangkau (kosong = tanpa preferensi)")
	maxAge := flag.Int("max-age-days", 75, "abaikan lowongan lebih tua dari ini")
	flag.Parse()

	if *cvPath == "" {
		fmt.Println("pakai: probe -cv <berkas cv> [-sources a,b] [-keep N] [-country Indonesia]")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*cvPath)
	if err != nil {
		log.Fatalf("baca CV: %v", err)
	}
	cvText := strings.TrimSpace(string(raw))
	if len(cvText) < 200 {
		log.Fatalf("teks CV terlalu pendek (%d karakter)", len(cvText))
	}

	profile := jobs.NewProfile(cvText)
	keywords := profile.Tokens()
	if len(keywords) > 12 {
		keywords = keywords[:12]
	}
	fmt.Printf("CV %d karakter · kata kunci teratas: %s\n\n", len(cvText), strings.Join(keywords, ", "))

	sources := jobs.Select(strings.Split(*sourceNames, ","))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	all := []jobs.Job{}
	for _, src := range sources {
		started := time.Now()
		list, err := src.Fetch(ctx, jobs.Query{Keywords: keywords, Location: ""})
		if err != nil {
			fmt.Printf("  %-10s GAGAL (%s): %v\n", src.Name(), time.Since(started).Round(time.Millisecond), err)
			continue
		}
		byCountry := map[string]int{}
		for _, j := range list {
			byCountry[countryOf(j.Location)]++
		}
		top := topCountries(byCountry, 4)
		fmt.Printf("  %-10s %4d lowongan dalam %s · negara teratas: %s\n",
			src.Name(), len(list), time.Since(started).Round(time.Millisecond), top)
		all = append(all, list...)
	}

	candidates := jobs.FilterAndRank(profile, all, *keep, *maxAge, *country)
	fmt.Printf("\n%d lowongan terkumpul → %d kandidat lolos saring untuk dinilai model\n\n", len(all), len(candidates))
	for i, j := range candidates {
		reach, factor := jobs.Reachability(j, *country)
		remote := ""
		if j.Remote {
			remote = " [remote]"
		}
		fmt.Printf("%2d. skor relevansi %6.1f (faktor %.2f, lolos=%v)\n", i+1, profile.Relevance(j), factor, reach)
		fmt.Printf("    %s\n", j.Title)
		fmt.Printf("    %s · %s%s · %s\n", j.Company, orNone(j.Location), remote, j.Source)
		fmt.Printf("    %s\n", j.URL)
	}
}

// countryOf pulls a rough country out of a location string, for the per-source
// summary. It is a display heuristic, not a decision.
func countryOf(location string) string {
	loc := strings.ToLower(strings.TrimSpace(location))
	if loc == "" {
		return "(kosong)"
	}
	if strings.Contains(loc, "indonesia") || strings.HasSuffix(loc, "jakarta") {
		return "Indonesia"
	}
	parts := strings.Split(loc, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return loc
	}
	return last
}

func topCountries(counts map[string]int, n int) string {
	type pair struct {
		name string
		n    int
	}
	list := make([]pair, 0, len(counts))
	for name, c := range counts {
		list = append(list, pair{name, c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].name < list[j].name
	})
	if len(list) > n {
		list = list[:n]
	}
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, fmt.Sprintf("%s=%d", p.name, p.n))
	}
	return strings.Join(out, ", ")
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(lokasi tidak dicantumkan)"
	}
	return s
}
