export interface CountryOption {
  code: string
  label: string
  name: string
}

const countryNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })

export function countryFlag(code: string): string {
  const normalized = code.toUpperCase()
  if (!/^[A-Z]{2}$/.test(normalized)) return ''
  return String.fromCodePoint(
    ...normalized.split('').map((char) => 0x1f1e6 + char.charCodeAt(0) - 65),
  )
}

export async function loadCountries(): Promise<CountryOption[]> {
  const { Country } = await import('country-state-city')
  return Country.getAllCountries()
    .map((country) => ({
      code: country.isoCode,
      label: countryNames.of(country.isoCode) ?? country.name,
      name: country.name,
    }))
    .toSorted((a, b) => a.label.localeCompare(b.label, 'zh-CN'))
}

export async function loadCities(countryCode: string): Promise<string[]> {
  if (!countryCode) return []
  const { City } = await import('country-state-city')
  return [
    ...new Set(
      City.getCitiesOfCountry(countryCode)
        ?.map((city) => city.name.trim())
        .filter(Boolean) ?? [],
    ),
  ].toSorted((a, b) => a.localeCompare(b))
}
