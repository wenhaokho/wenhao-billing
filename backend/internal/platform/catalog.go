package platform

import "github.com/LOQATO/gokit/i18n"

// Catalog is the app's Indonesian: every sentence the app (and apierr) shows,
// keyed by its English template. TestCatalog fails on a sentence with no
// entry and on an entry nothing uses.
var Catalog = &i18n.Catalog{
	Sentences: map[string]string{
		// apierr's framework errors.
		"Bad request":                  "Permintaan tidak valid",
		"Not authenticated":            "Belum masuk",
		"Forbidden":                    "Tidak diizinkan",
		"Not found":                    "Tidak ditemukan",
		"Method not allowed":           "Metode tidak diizinkan",
		"Request body too large":       "Isi permintaan terlalu besar",
		"Unsupported media type":       "Jenis media tidak didukung",
		"Request validation failed":    "Validasi permintaan gagal",
		"An unexpected error occurred": "Terjadi kesalahan tak terduga",

		// platform
		"You do not have permission to do this": "Tidak memiliki izin untuk tindakan ini",
	},
	Words: map[string]string{},
	Nouns: map[string]string{},
}
