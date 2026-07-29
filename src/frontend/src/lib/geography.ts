import cityDataUrl from 'country-state-city/lib/assets/city.json?url'
import countryDataUrl from 'country-state-city/lib/assets/country.json?url'

export interface CountryOption {
  code: string
  label: string
  name: string
}

export interface GeographyCity {
  name: string
  latitude: string | null
  longitude: string | null
}

export interface GeographyCountry {
  isoCode: string
  name: string
  latitude?: string | null
  longitude?: string | null
}

type CityRecord = [
  name: string,
  countryCode: string,
  stateCode: string,
  latitude: string | null,
  longitude: string | null,
]

export interface GeographyCoordinates {
  citiesByCountry: ReadonlyMap<string, GeographyCity[]>
  countriesByCode: ReadonlyMap<string, GeographyCountry>
}

const countryNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
let citiesByCountryPromise: Promise<Map<string, GeographyCity[]>> | null = null
let countriesPromise: Promise<GeographyCountry[]> | null = null

async function fetchGeographyAsset<T>(url: string): Promise<T> {
  const response = await fetch(url)
  if (!response.ok) throw new Error(`Unable to load geography data (${response.status})`)
  return response.json() as Promise<T>
}

function loadCityIndex(): Promise<Map<string, GeographyCity[]>> {
  if (citiesByCountryPromise) return citiesByCountryPromise

  citiesByCountryPromise = fetchGeographyAsset<CityRecord[]>(cityDataUrl)
    .then((cities) => {
      const citiesByCountry = new Map<string, GeographyCity[]>()
      cities.forEach(([name, countryCode, , latitude, longitude]) => {
        const countryCities = citiesByCountry.get(countryCode) ?? []
        countryCities.push({ name, latitude, longitude })
        citiesByCountry.set(countryCode, countryCities)
      })
      return citiesByCountry
    })
    .catch((error: unknown) => {
      citiesByCountryPromise = null
      throw error
    })

  return citiesByCountryPromise
}

function loadCountryRecords(): Promise<GeographyCountry[]> {
  if (countriesPromise) return countriesPromise

  countriesPromise = fetchGeographyAsset<GeographyCountry[]>(countryDataUrl)
    .catch((error: unknown) => {
      countriesPromise = null
      throw error
    })
  return countriesPromise
}

export function countryFlag(code: string): string {
  const normalized = code.toUpperCase()
  if (!/^[A-Z]{2}$/.test(normalized)) return ''
  return String.fromCodePoint(
    ...normalized.split('').map((char) => 0x1f1e6 + char.charCodeAt(0) - 65),
  )
}

export async function loadCountries(): Promise<CountryOption[]> {
  const countries = await loadCountryRecords()
  return countries
    .map((country) => ({
      code: country.isoCode,
      label: countryNames.of(country.isoCode) ?? country.name,
      name: country.name,
    }))
    .toSorted((a, b) => a.label.localeCompare(b.label, 'zh-CN'))
}

export async function loadCities(countryCode: string): Promise<string[]> {
  if (!countryCode) return []
  const citiesByCountry = await loadCityIndex()
  return [
    ...new Set(
      (citiesByCountry.get(countryCode.toUpperCase()) ?? [])
        .map((city) => city.name.trim())
        .filter(Boolean),
    ),
  ].toSorted((a, b) => a.localeCompare(b))
}

export async function loadGeographyCoordinates(): Promise<GeographyCoordinates> {
  const [citiesByCountry, countries] = await Promise.all([
    loadCityIndex(),
    loadCountryRecords(),
  ])
  return {
    citiesByCountry,
    countriesByCode: new Map(countries.map((country) => [country.isoCode, country])),
  }
}
