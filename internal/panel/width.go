package panel

import "unicode"

// width returns a string's terminal display width. CJK text is double-width,
// and the panel's columns are unreadable without accounting for it.
func width(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0 // combining marks and format characters take no cells
	case isWide(r):
		return 2
	default:
		return 1
	}
}

// isWide covers the East Asian Wide and Fullwidth ranges the panel actually
// prints: CJK, kana, fullwidth forms, and the box/arrow glyphs used in the
// table. It is not a complete Unicode implementation and does not need to be.
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, kangxi, punctuation
		r >= 0x3041 && r <= 0x33FF, // kana, CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD: // CJK ext B+
		return true
	}
	return false
}

// pad right-pads s to n display columns.
func pad(s string, n int) string {
	d := n - width(s)
	if d <= 0 {
		return s
	}
	b := make([]byte, d)
	for i := range b {
		b[i] = ' '
	}
	return s + string(b)
}

// padLeft left-pads s to n display columns.
func padLeft(s string, n int) string {
	d := n - width(s)
	if d <= 0 {
		return s
	}
	b := make([]byte, d)
	for i := range b {
		b[i] = ' '
	}
	return string(b) + s
}
