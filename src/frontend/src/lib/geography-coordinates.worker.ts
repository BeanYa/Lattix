import cityDataUrl from 'country-state-city/lib/assets/city.json?url'
import countryDataUrl from 'country-state-city/lib/assets/country.json?url'

import type {
  GeographyCity,
  GeographyCountry,
  GeographyLocationRequest,
  GeographyLocationResult,
} from './geography'

type CityRecord = [
  name: string,
  countryCode: string,
  stateCode: string,
  latitude: string | null,
  longitude: string | null,
]

const workerScope = self as unknown as {
  onmessage: ((event: MessageEvent<GeographyLocationRequest[]>) => void) | null
  postMessage: (message: GeographyLocationResult[]) => void
}

function normalizePlace(value: string) {
  return value.trim().toLocaleLowerCase().replace(/[\s_-]+/g, '')
}

function finiteCoordinate(value: string | null | undefined) {
  if (value == null || value.trim() === '') return null
  const coordinate = Number(value)
  return Number.isFinite(coordinate) ? coordinate : null
}

async function fetchAsset<T>(url: string): Promise<T> {
  const response = await fetch(url)
  if (!response.ok) throw new Error(`Unable to load geography data (${response.status})`)
  return response.json() as Promise<T>
}

workerScope.onmessage = async (event) => {
  const requests = event.data
  const requestedCountries = new Set(requests.map((request) => request.countryCode))

  try {
    const [cityRecords, countries] = await Promise.all([
      fetchAsset<CityRecord[]>(cityDataUrl),
      fetchAsset<GeographyCountry[]>(countryDataUrl),
    ])
    const citiesByCountry = new Map<string, GeographyCity[]>()
    cityRecords.forEach(([name, countryCode, , latitude, longitude]) => {
      if (!requestedCountries.has(countryCode)) return
      const cities = citiesByCountry.get(countryCode) ?? []
      cities.push({ name, latitude, longitude })
      citiesByCountry.set(countryCode, cities)
    })
    const countriesByCode = new Map(countries.map((country) => [country.isoCode, country]))

    const results: GeographyLocationResult[] = requests.map((request) => {
      const place = normalizePlace(request.location)
      const city = place
        ? (citiesByCountry.get(request.countryCode) ?? []).find((candidate) => {
            const candidateName = normalizePlace(candidate.name)
            return candidateName === place || (place.length > 2 && candidateName.includes(place))
          })
        : undefined
      const country = countriesByCode.get(request.countryCode)
      return {
        key: request.key,
        lat: finiteCoordinate(city?.latitude) ?? finiteCoordinate(country?.latitude),
        lng: finiteCoordinate(city?.longitude) ?? finiteCoordinate(country?.longitude),
      }
    })

    workerScope.postMessage(results)
  } catch {
    workerScope.postMessage(requests.map((request) => ({ key: request.key, lat: null, lng: null })))
  }
}
