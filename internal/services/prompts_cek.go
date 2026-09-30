package services

import "fmt"

// buildCekPrompt is the self-service prompt: job description in, honest reading
// of the CV out, in Indonesian.
//
// Two deliberate constraints:
//   - The job description IS the rubric here. The recruiter-side pipeline
//     retrieves a rubric corpus; a visitor pasting a posting has already given
//     us the only criteria that matter, so no retrieval is involved.
//   - No promises about getting hired, and no inventing experience the CV does
//     not have. The value is an honest gap list, not encouragement.
func buildCekPrompt(jobTitle, jobDesc, cvText string) string {
	return fmt.Sprintf(`Kamu adalah perekrut teknis berpengalaman di Indonesia yang membaca CV dengan kritis dan jujur.

LOWONGAN YANG DILAMAR (judul: %s):
%s

CV PELAMAR:
%s

TUGAS:
1. Bandingkan CV dengan lowongan di atas. Nilai seberapa cocok CV ini, seolah kamu yang menyaring berkasnya.
2. Beri skor 0-100 (skor) dengan pedoman:
   - 0-20 = tidak nyambung, keahlian utama tidak ada
   - 21-45 = ada sedikit kesamaan, tapi syarat penting tidak terbukti
   - 46-65 = cukup cocok, sebagian besar syarat terlihat
   - 66-80 = cocok, bukti hasil kerja jelas dan relevan
   - 81-100 = sangat cocok, melebihi syarat di beberapa bagian
3. Temukan TEPAT 3 celah paling menentukan yang membuat CV ini kalah sebelum dibaca manusia.
   Untuk setiap celah sebutkan: bagian mana (bagian), apa masalahnya (masalah), dan apa yang harus dilakukan pelamar (perbaikan).
4. Tulis satu contoh perbaikan nyata: satu baris dari CV yang lemah (sebelum), versi perbaikannya (sesudah), dan alasan singkatnya (alasan). Ambil kalimat asli dari CV pelamar.

ATURAN KETAT:
- Jangan mengarang pengalaman, keahlian, angka, atau pencapaian yang tidak ada di CV.
- Jangan menjanjikan pelamar akan diterima kerja atau dipanggil interview.
- Jangan menyarankan menambahkan hal yang tidak dimiliki pelamar; sarankan cara menyajikan yang sudah ada dengan lebih jelas.
- Kalau CV terlihat tidak nyambung dengan lowongan, katakan apa adanya.
- Tulis semuanya dalam bahasa Indonesia yang sederhana dan langsung. Hindari istilah pemasaran dan kata berlebihan.
- Jangan menyebut nama pelamar di dalam ringkasan.

FORMAT KELUARAN (kembalikan HANYA JSON yang valid, tanpa markdown, tanpa penjelasan tambahan, pastikan JSON lengkap dan tertutup):
{
  "skor": 62,
  "ringkasan": "2-3 kalimat: kenapa skornya segitu dan apa yang paling menentukan.",
  "celah": [
    {"bagian": "...", "masalah": "...", "perbaikan": "..."},
    {"bagian": "...", "masalah": "...", "perbaikan": "..."},
    {"bagian": "...", "masalah": "...", "perbaikan": "..."}
  ],
  "contoh_perbaikan": {"sebelum": "...", "sesudah": "...", "alasan": "..."}
}`,
		jobTitle, jobDesc, cvText)
}
