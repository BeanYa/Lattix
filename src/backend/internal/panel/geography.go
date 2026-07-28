package panel

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const maxServerLocationRunes = 100

func normalizeServerGeography(countryCode, location string) (string, string, error) {
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	location = strings.TrimSpace(location)
	if countryCode == "" {
		return "", "", fmt.Errorf("country_code 不能为空")
	}
	region, err := language.ParseRegion(countryCode)
	if err != nil || len(countryCode) != 2 {
		return "", "", fmt.Errorf("country_code 须为有效的 ISO 3166-1 两位代码")
	}
	if region.String() != countryCode || countryCode == "ZZ" {
		return "", "", fmt.Errorf("country_code 须为有效的 ISO 3166-1 两位代码")
	}
	if utf8.RuneCountInString(location) > maxServerLocationRunes {
		return "", "", fmt.Errorf("location 不能超过 %d 个字符", maxServerLocationRunes)
	}
	return countryCode, location, nil
}

func countryName(countryCode string) string {
	region, err := language.ParseRegion(countryCode)
	if err != nil {
		return ""
	}
	return display.SimplifiedChinese.Regions().Name(region)
}

func countryFlag(countryCode string) string {
	code := strings.ToUpper(countryCode)
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(code[0]-'A'), 0x1F1E6 + rune(code[1]-'A')})
}
