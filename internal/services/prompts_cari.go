package services

import (
	"fmt"
	"strings"
)

// buildCariPrompt judges ONE posting against the CV. One posting per call is
// deliberate: a batch prompt would be cheaper per posting but the scores drift
// between runs as soon as the batch composition changes, and consistency is the
// one thing this product can honestly promise.
func buildCariPrompt(cvText, title, company, location string, tags []string, description string) string {
	tagLine := strings.Join(tags, ", ")
	if tagLine == "" {
		tagLine = "(tidak ada tag)"
	}
	if strings.TrimSpace(company) == "" {
		company = "(tidak disebutkan)"
	}
	if strings.TrimSpace(location) == "" {
		location = "(tidak disebutkan)"
	}
	return fmt.Sprintf(`Kamu perekrut teknis berpengalaman di Indonesia. Tugasmu menilai satu lowongan terhadap satu CV: seberapa cocok pelamar ini, dan apa yang membuat CV-nya kalah kalau dia melamar.

LOWONGAN
Judul: %s
Perusahaan: %s
Lokasi: %s
Tag: %s
Isi lowongan:
%s

CV PELAMAR:
%s

TUGAS
1. Beri skor 0-100 (skor) untuk kecocokan CV ini dengan lowongan ini:
   - 0-20 = tidak cocok, keahlian utama yang diminta tidak ada di CV
   - 21-45 = ada sedikit kesamaan, syarat penting tidak terbukti
   - 46-65 = cukup cocok, sebagian besar syarat terlihat
   - 66-80 = cocok, bukti hasil kerja jelas dan relevan
   - 81-100 = sangat cocok, melebihi syarat di beberapa bagian
2. Tulis alasan (alasan) 2-3 kalimat: kenapa skornya segitu untuk lowongan INI, dan apa yang paling menentukan.
3. Sebutkan paling banyak 2 celah (celah) yang paling menentukan untuk lowongan INI, masing-masing satu kalimat pendek dan konkret.

ATURAN KETAT
- Jangan mengarang pengalaman, keahlian, atau angka yang tidak ada di CV.
- Jangan menjanjikan pelamar akan diterima atau dipanggil interview.
- Kalau lowongannya memang tidak cocok, katakan tidak cocok — jangan memaksakan skor tinggi.
- Kalau lokasi atau izin kerja jadi penghalang nyata, sebutkan di celah.
- Tulis dalam bahasa Indonesia yang sederhana, tanpa istilah pemasaran.

FORMAT KELUARAN (kembalikan HANYA JSON valid, tanpa markdown, tanpa penjelasan tambahan):
{
  "skor": 72,
  "alasan": "2-3 kalimat bahasa Indonesia.",
  "celah": ["satu kalimat", "satu kalimat"]
}`,
		title, company, location, tagLine, headText(description, 4000), cvText)
}

// headText keeps a prompt within a sane size without pulling the whole posting
// into every call. The services package already has truncate, but that one caps
// an error message for a log line; this one caps document text for a prompt.
func headText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
