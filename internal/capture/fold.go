package capture

import "strings"

var folder = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ö", "o", "ő", "o", "ú", "u", "ü", "u", "ű", "u",
	"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ö", "o", "Ő", "o", "Ú", "u", "Ü", "u", "Ű", "u",
)

// Fold lowercases s and strips Hungarian accents, so the keyword
// "elérhetőség" matches the path "/elerhetoseg".
func Fold(s string) string { return folder.Replace(strings.ToLower(s)) }
