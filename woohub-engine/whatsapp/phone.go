package whatsapp

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

var nonDigitsRegex = regexp.MustCompile(`\D`)

// NormalizeDigits extracts only numbers from a phone string
func NormalizeDigits(phone string) string {
	return nonDigitsRegex.ReplaceAllString(phone, "")
}

// FormatE164 formats phone as +<digits>
func FormatE164(phone string) string {
	digits := NormalizeDigits(phone)
	if digits == "" {
		return ""
	}
	if (len(digits) == 10 || len(digits) == 11) && !strings.HasPrefix(digits, "55") {
		digits = "55" + digits
	}
	return "+" + digits
}

// GenerateBrazilianVariants returns all plausible Brazilian phone variants (with and without 9th digit)
func GenerateBrazilianVariants(phone string) []string {
	digits := NormalizeDigits(phone)
	if digits == "" {
		return nil
	}

	if (len(digits) == 10 || len(digits) == 11) && !strings.HasPrefix(digits, "55") {
		digits = "55" + digits
	}

	variants := []string{digits}

	if strings.HasPrefix(digits, "55") && (len(digits) == 12 || len(digits) == 13) {
		ddd := digits[2:4]
		rest := digits[4:]
		if len(digits) == 13 && rest[0] == '9' {
			// Has 9 -> generate without 9
			without9 := "55" + ddd + rest[1:]
			variants = append(variants, without9)
		} else if len(digits) == 12 {
			// Doesn't have 9 -> generate with 9
			with9 := "55" + ddd + "9" + rest
			variants = append(variants, with9)
		}
	}

	return variants
}

// ResolveCanonicalJID queries WhatsApp server via IsOnWhatsApp to get the exact JID
func ResolveCanonicalJID(ctx context.Context, client *whatsmeow.Client, phoneOrJid string) (types.JID, error) {
	if client == nil {
		return types.EmptyJID, fmt.Errorf("client is nil")
	}

	// If already a group JID, return as is
	if strings.Contains(phoneOrJid, "@g.us") {
		return types.ParseJID(phoneOrJid)
	}

	// If it's already a full JID, check if it's newsletter or broadcast
	if strings.Contains(phoneOrJid, "@") {
		return types.ParseJID(phoneOrJid)
	}

	variants := GenerateBrazilianVariants(phoneOrJid)
	if len(variants) == 0 {
		return types.EmptyJID, fmt.Errorf("invalid phone number: %s", phoneOrJid)
	}

	// Query WhatsApp servers
	resp, err := client.IsOnWhatsApp(variants)
	if err == nil && len(resp) > 0 {
		for _, item := range resp {
			if item.IsIn && !item.JID.IsEmpty() {
				return item.JID, nil
			}
		}
	}

	// Fallback to first variant as standard user JID
	return types.NewJID(variants[0], types.DefaultUserServer), nil
}
