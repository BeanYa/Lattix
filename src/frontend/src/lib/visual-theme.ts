export interface EarthPalette {
  oceanColor: string
  oceanEmissive: string
  oceanHue: number
  oceanSaturation: number
  oceanLightness: number
  land: string
  landEmissive: string
  landSide: string
  landSideEmissive: string
  cloud: string
  atmosphere: string
  nodeOnline: string
  nodeOffline: string
  nodeWarning: string
  linkActive: string
  linkDegraded: string
  linkFailed: string
  linkPending: string
  ring: string
}

export const DEFAULT_EARTH_PALETTE: EarthPalette = {
  oceanColor: '#ffffff',
  oceanEmissive: '#075a80',
  oceanHue: 0.535,
  oceanSaturation: 0.74,
  oceanLightness: 0.48,
  land: '#a7ec35',
  landEmissive: '#284d17',
  landSide: '#55a927',
  landSideEmissive: '#193d13',
  cloud: '#fffbe6',
  atmosphere: '#a9efff',
  nodeOnline: '#7ce5b2',
  nodeOffline: '#ff8d87',
  nodeWarning: '#ffd45f',
  linkActive: '#f7ffb2',
  linkDegraded: '#ffbf3f',
  linkFailed: '#ff7771',
  linkPending: '#d8d2ff',
  ring: '#d9f47d',
}

export function readEarthPalette(): EarthPalette {
  const styles = getComputedStyle(document.documentElement)
  const color = (name: string, fallback: string) => styles.getPropertyValue(name).trim() || fallback
  const number = (name: string, fallback: number) => {
    const value = Number(styles.getPropertyValue(name).trim())
    return Number.isFinite(value) ? value : fallback
  }

  return {
    oceanColor: color('--earth-ocean-color', DEFAULT_EARTH_PALETTE.oceanColor),
    oceanEmissive: color('--earth-ocean-emissive', DEFAULT_EARTH_PALETTE.oceanEmissive),
    oceanHue: number('--earth-ocean-hue', DEFAULT_EARTH_PALETTE.oceanHue),
    oceanSaturation: number('--earth-ocean-saturation', DEFAULT_EARTH_PALETTE.oceanSaturation),
    oceanLightness: number('--earth-ocean-lightness', DEFAULT_EARTH_PALETTE.oceanLightness),
    land: color('--earth-land', DEFAULT_EARTH_PALETTE.land),
    landEmissive: color('--earth-land-emissive', DEFAULT_EARTH_PALETTE.landEmissive),
    landSide: color('--earth-land-side', DEFAULT_EARTH_PALETTE.landSide),
    landSideEmissive: color('--earth-land-side-emissive', DEFAULT_EARTH_PALETTE.landSideEmissive),
    cloud: color('--earth-cloud', DEFAULT_EARTH_PALETTE.cloud),
    atmosphere: color('--earth-atmosphere', DEFAULT_EARTH_PALETTE.atmosphere),
    nodeOnline: color('--earth-node-online', DEFAULT_EARTH_PALETTE.nodeOnline),
    nodeOffline: color('--earth-node-offline', DEFAULT_EARTH_PALETTE.nodeOffline),
    nodeWarning: color('--earth-node-warning', DEFAULT_EARTH_PALETTE.nodeWarning),
    linkActive: color('--earth-link-active', DEFAULT_EARTH_PALETTE.linkActive),
    linkDegraded: color('--earth-link-degraded', DEFAULT_EARTH_PALETTE.linkDegraded),
    linkFailed: color('--earth-link-failed', DEFAULT_EARTH_PALETTE.linkFailed),
    linkPending: color('--earth-link-pending', DEFAULT_EARTH_PALETTE.linkPending),
    ring: color('--earth-ring', DEFAULT_EARTH_PALETTE.ring),
  }
}
