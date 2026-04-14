package marketplace

import "strings"

type Descriptor struct {
	ID                string
	Name              string
	ProviderFamily    string
	CountryCode       string
	SupportsMessaging bool
}

var descriptors = []Descriptor{
	{ID: "marktplaats", Name: "Marktplaats", ProviderFamily: "marktplaats", CountryCode: "NL", SupportsMessaging: true},
	{ID: "vinted_nl", Name: "Vinted NL", ProviderFamily: "vinted", CountryCode: "NL"},
	{ID: "olxbg", Name: "OLX Bulgaria", ProviderFamily: "olx", CountryCode: "BG"},
	{ID: "vinted_dk", Name: "Vinted DK", ProviderFamily: "vinted", CountryCode: "DK"},
}

var legacyMarketplaceIDs = map[string]string{
	"vinted": "vinted_nl",
}

func NormalizeMarketplaceID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if canonical, ok := legacyMarketplaceIDs[id]; ok {
		return canonical
	}
	return id
}

func DescriptorByID(id string) (Descriptor, bool) {
	id = NormalizeMarketplaceID(id)
	for _, descriptor := range descriptors {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

func AllDescriptors() []Descriptor {
	out := make([]Descriptor, len(descriptors))
	copy(out, descriptors)
	return out
}

func CountryDefaultMarketplaces(countryCode string) []string {
	switch strings.ToUpper(strings.TrimSpace(countryCode)) {
	case "NL":
		return []string{"marktplaats", "vinted_nl"}
	case "BG":
		return []string{"olxbg"}
	case "DK":
		return []string{"vinted_dk"}
	default:
		return nil
	}
}

func ScopeCandidates(countryCode string, crossBorder bool) []string {
	if crossBorder {
		out := make([]string, 0, len(descriptors))
		for _, descriptor := range descriptors {
			out = append(out, descriptor.ID)
		}
		return out
	}
	return CountryDefaultMarketplaces(countryCode)
}

func ValidateScope(countryCode string, crossBorder bool, requested []string) []string {
	allowed := ScopeCandidates(countryCode, crossBorder)
	if len(allowed) == 0 {
		return nil
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = true
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(requested))
	for _, value := range requested {
		value = NormalizeMarketplaceID(value)
		if !allowedSet[value] || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return append([]string(nil), CountryDefaultMarketplaces(countryCode)...)
	}
	return out
}
