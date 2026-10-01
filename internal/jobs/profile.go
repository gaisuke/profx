package jobs

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// timeFromUnix converts the epoch seconds Arbeitnow uses.
func timeFromUnix(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// shortTech keeps two-letter tokens that matter in this domain. "go", "js" and
// "sql" are the difference between a matching posting and a discarded one, and a
// blanket "drop anything under three characters" rule throws them away.
var shortTech = map[string]bool{
	"go": true, "js": true, "ts": true, "sql": true, "aws": true, "gcp": true,
	"ui": true, "ux": true, "qa": true, "ml": true, "ai": true, "bi": true,
	"api": true, "css": true, "php": true, "k8s": true, "etl": true, "sre": true,
	"r": true, "c": true, "hr": true, "seo": true, "ios": true, "dba": true,
	"c++": true, "c#": true, "ci": true, "cd": true, "et": true, "ba": true,
}

// stopwords are the words that appear in every CV and every posting and
// therefore carry no signal. Indonesian and English, because the postings are
// mixed and so are the CVs.
var stopwords = map[string]bool{
	"dan": true, "atau": true, "yang": true, "untuk": true, "dengan": true, "pada": true,
	"adalah": true, "akan": true, "bisa": true, "dari": true, "dalam": true, "ini": true,
	"itu": true, "juga": true, "saya": true, "kami": true, "kamu": true, "anda": true,
	"tidak": true, "ada": true, "para": true, "oleh": true, "agar": true, "serta": true,
	"sebagai": true, "ke": true, "di": true, "the": true, "and": true, "for": true,
	"with": true, "you": true, "are": true, "our": true, "will": true, "have": true,
	"that": true, "this": true, "from": true, "your": true, "who": true, "all": true,
	"work": true, "working": true, "team": true, "teams": true, "role": true, "job": true,
	"years": true, "year": true, "experience": true, "pengalaman": true, "kerja": true,
	"membuat": true, "menggunakan": true, "tahun": true, "keahlian": true, "skills": true,
	"nama": true, "email": true, "phone": true, "university": true, "universitas": true,
	"pendidikan": true, "education": true, "lain": true, "lainnya": true, "etc": true,
	"about": true, "more": true, "other": true, "others": true, "must": true, "able": true,
	"minimum": true, "minimal": true, "plus": true, "plusnya": true, "etc.": true,
	"perusahaan": true, "company": true, "kandidat": true, "candidate": true,
	"bertanggung": true, "jawab": true, "responsibilities": true, "requirements": true,
	"kualifikasi": true, "qualifications": true, "tanggung": true, "deskripsi": true,
	"and/or": true, "strong": true, "baik": true, "bagus": true, "mampu": true,
}

var tokenRE = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+#.]*`)

// Profile is what we know about the candidate, reduced to weighted keywords.
// It exists so that filtering postings costs no model calls.
type Profile struct {
	Keywords map[string]int
}

// NewProfile turns CV text into weighted keywords. Repetition raises a term's
// weight, with a ceiling: a word repeated nine times in a CV is a habit of
// writing, not nine times more relevant than another skill.
func NewProfile(cvText string) Profile {
	weights := map[string]int{}
	for _, tok := range tokenRE.FindAllString(strings.ToLower(cvText), -1) {
		tok = strings.Trim(tok, ".")
		if tok == "" {
			continue
		}
		if stopwords[tok] {
			continue
		}
		if len([]rune(tok)) < 3 && !shortTech[tok] {
			continue
		}
		if weights[tok] < 4 {
			weights[tok]++
		}
	}
	return Profile{Keywords: weights}
}

// Tokens lists the profile's keywords, most weighted first, for logs and tests.
func (p Profile) Tokens() []string {
	out := make([]string, 0, len(p.Keywords))
	for k := range p.Keywords {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if p.Keywords[out[i]] != p.Keywords[out[j]] {
			return p.Keywords[out[i]] > p.Keywords[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// Relevance scores a posting against the profile without calling a model: a
// title match means far more than a mention buried in the body, which is what
// the weights say.
func (p Profile) Relevance(j Job) float64 {
	titleHits, tagHits := 0, 0
	title := strings.ToLower(j.Title)
	for _, tag := range j.Tags {
		tag = strings.ToLower(tag)
		if p.Keywords[tag] > 0 {
			tagHits += p.Keywords[tag]
			continue
		}
		// Tags are often phrases ("machine learning"); count a hit per word.
		for _, w := range tokenRE.FindAllString(tag, -1) {
			if p.Keywords[w] > 0 {
				tagHits++
			}
		}
	}
	seen := map[string]bool{}
	for _, w := range tokenRE.FindAllString(title, -1) {
		if p.Keywords[w] > 0 && !seen[w] {
			seen[w] = true
			titleHits += p.Keywords[w]
		}
	}
	desc := strings.ToLower(j.Description)
	descHits := 0
	seen = map[string]bool{}
	for _, w := range tokenRE.FindAllString(desc, -1) {
		if !seen[w] && p.Keywords[w] > 0 {
			seen[w] = true
			descHits += p.Keywords[w]
		}
	}
	if descHits > 40 { // one long posting must not outweigh a matching title
		descHits = 40
	}
	score := float64(6*titleHits + 3*tagHits + descHits)
	return score * titleCoverage(p, title)
}

// titleCoverage measures how much of the posting's own job title the candidate
// has any claim to, and discounts the rest. A live run with a backend CV filled
// four of six short-list slots with "Data Engineer" roles: the words "engineer"
// and "postgresql" matched, and the word "data" — the one that actually decides
// the role — did not. The model scored them low, correctly, but the slots were
// already spent.
//
// The discount is deliberately mild (0.6 at worst): a title using a synonym the
// CV never needed should not be buried, only ranked below a role it plainly is.
func titleCoverage(p Profile, title string) float64 {
	tokens := map[string]bool{}
	for _, w := range tokenRE.FindAllString(title, -1) {
		if stopwords[w] || (len([]rune(w)) < 3 && !shortTech[w]) {
			continue
		}
		tokens[w] = true
	}
	if len(tokens) == 0 {
		return 1
	}
	matched := 0
	for w := range tokens {
		if p.Keywords[w] > 0 {
			matched++
		}
	}
	coverage := float64(matched) / float64(len(tokens))
	return 0.6 + 0.4*coverage
}

// FilterAndRank drops what is clearly irrelevant, removes duplicates, keeps the
// freshest postings, and returns the best candidates for the model.
//
// The thresholds are deliberately loose: this step exists to protect the model
// budget, not to make the final decision. A posting with a matching title and
// nothing else still gets scored, because the model is the one judging fit.
func FilterAndRank(profile Profile, in []Job, keep int, maxAgeDays int, preferCountry string) []Job {
	if keep <= 0 {
		keep = 10
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	scored := make([]struct {
		job    Job
		score  float64
		factor float64
	}, 0, len(in))
	seen := map[string]bool{}
	for _, j := range in {
		if maxAgeDays > 0 && !j.PublishedAt.IsZero() && j.PublishedAt.Before(cutoff) {
			continue
		}
		if strings.TrimSpace(j.Title) == "" || strings.TrimSpace(j.URL) == "" {
			continue
		}
		key := dedupeKey(j)
		if seen[key] {
			continue
		}
		seen[key] = true
		score := profile.Relevance(j)
		if score <= 0 {
			continue
		}
		keepIt, factor := Reachability(j, preferCountry)
		if !keepIt {
			continue
		}
		scored = append(scored, struct {
			job    Job
			score  float64
			factor float64
		}{j, score * factor, factor})
	}
	// A close call goes to the posting the candidate can actually take. Without
	// this, a keyword-perfect on-site role abroad ties with a local one and wins
	// on input order — which is exactly what the first live run did.
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i], scored[j]
		if hi := math.Max(a.score, b.score); hi > 0 && math.Abs(a.score-b.score) <= 0.15*hi && a.factor != b.factor {
			return a.factor > b.factor
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.job.PublishedAt.After(b.job.PublishedAt)
	})
	if len(scored) > keep {
		scored = scored[:keep]
	}
	out := make([]Job, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.job)
	}
	return out
}

// indonesianHints are the place words that appear in postings for jobs a
// candidate in Indonesia can actually take.
var indonesianHints = []string{
	"indonesia", "jakarta", "bandung", "surabaya", "tangerang", "bekasi", "depok",
	"bogor", "ciputat", "yogyakarta", "jogja", "semarang", "solo", "malang",
	"denpasar", "bali", "medan", "makassar", "batam", "balikpapan", "samarinda",
	"palembang", "pekanbaru", "manado", "pontianak", "asia", "apac",
}

// Reachability decides whether a posting survives at all, and with what weight.
// A perfect keyword match for an on-site role in Berlin is worse than a good
// match in Jakarta: the first live run proved it, ranking correctly on keywords
// and recommending German on-site work to a candidate in Jakarta.
//
// With a preferred country set (the local-first deployment), unreachable
// postings are dropped rather than discounted, because a discount still lets them
// into a short list that has no room for them:
//
//	local posting                     keep, full weight
//	remote, eligibility says open     keep, full weight
//	remote, eligibility unstated      keep, discounted — may surface if nothing
//	                                  better exists
//	remote, eligibility says closed   drop
//	on-site abroad or unknown         drop
//
// Without a preferred country the gate stays out of the way and only orders what
// it is given.
func Reachability(j Job, preferCountry string) (bool, float64) {
	loc := strings.ToLower(j.Location)
	prefer := strings.ToLower(strings.TrimSpace(preferCountry))
	if prefer == "" {
		if j.Remote || loc == "" {
			return true, 1.0
		}
		for _, hint := range indonesianHints {
			if strings.Contains(loc, hint) {
				return true, 0.95
			}
		}
		return true, 0.6
	}
	if strings.Contains(loc, prefer) {
		return true, 1.0
	}
	for _, hint := range indonesianHints {
		if strings.Contains(loc, hint) {
			return true, 1.0
		}
	}
	if !j.Remote {
		return false, 0 // on-site somewhere else: the candidate cannot take it
	}
	if !OpenToIndonesia(j.Location) {
		return false, 0
	}
	if explicitlyOpen(j.Location) {
		return true, 1.0
	}
	// Remote with nothing said about who may apply. Not dropped outright — some
	// boards simply leave the field empty — but it must not outrank a posting that
	// is definitely open.
	return true, 0.65
}

// explicitlyOpen reports whether a posting names a scope that includes Indonesia.
func explicitlyOpen(location string) bool {
	loc := strings.ToLower(location)
	for _, hint := range []string{"worldwide", "anywhere", "global", "asia", "apac", "asean", "indonesia", "southeast asia", "south-east asia"} {
		if strings.Contains(loc, hint) {
			return true
		}
	}
	return false
}

// dedupeKey collapses the same job posted on two boards: normalised title plus
// company. The URL differs between boards, so it cannot be the key.
var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func dedupeKey(j Job) string {
	t := nonAlnum.ReplaceAllString(strings.ToLower(j.Title), "")
	c := nonAlnum.ReplaceAllString(strings.ToLower(j.Company), "")
	return t + "|" + c
}
