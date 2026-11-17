import type { ExternalExit, ParseExternalLinkResponse, Server } from '@/lib/types'

// 未托管外部直链节点的协议表单（服务器创建/编辑共用）：与节点表单一样按协议展示
// 字段；分享链接只用于「解析 → 自动填写」，保存的是下面这些协议参数。
export const EXTERNAL_PROTOCOLS = [
  'vless',
  'vmess',
  'trojan',
  'shadowsocks',
  'socks',
  'http',
] as const

export const EXTERNAL_PROTOCOL_LABELS: Record<string, string> = {
  vless: 'VLESS',
  vmess: 'VMess',
  trojan: 'Trojan',
  shadowsocks: 'Shadowsocks',
  socks: 'SOCKS5',
  http: 'HTTP',
}

export const EXTERNAL_NETWORKS = ['tcp', 'grpc', 'xhttp', 'ws', 'httpupgrade']
export const EXTERNAL_FINGERPRINTS = [
  'chrome',
  'firefox',
  'safari',
  'edge',
  'ios',
  'android',
  '360',
  'qq',
  'random',
  'randomized',
]
export const EXTERNAL_FLOWS = [
  { value: 'xtls-rprx-vision', label: 'xtls-rprx-vision（vision）' },
  { value: 'none', label: '无 flow' },
]
export const EXTERNAL_ENCRYPTIONS = [
  { value: 'none', label: '无' },
  { value: 'mlkem768', label: 'mlkem768（后量子）' },
  { value: 'x25519', label: 'x25519' },
]
export const EXTERNAL_SS_METHODS = [
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
  '2022-blake3-chacha20-poly1305',
  'aes-128-gcm',
  'aes-256-gcm',
  'chacha20-ietf-poly1305',
]
export const EXTERNAL_VMESS_CIPHERS = ['auto', 'aes-128-gcm', 'chacha20-poly1305', 'none', 'zero']
export const EXTERNAL_XHTTP_MODES = ['auto', 'packet-up', 'stream-up']

const REALITY_NETWORKS = ['tcp', 'grpc', 'xhttp']

/** 外部出口协议是否有传输/安全层（与后端 SupportsTransport 同源）。 */
export function externalSupportsTransport(protocol: string): boolean {
  return protocol === 'vless' || protocol === 'vmess' || protocol === 'trojan'
}

/** 安全层可选项：reality 仅 tcp/grpc/xhttp；ws/httpupgrade 仅 tls/none。 */
export function externalSecurityOptions(protocol: string, network: string): string[] {
  if (!externalSupportsTransport(protocol)) return []
  return REALITY_NETWORKS.includes(network) ? ['reality', 'tls', 'none'] : ['tls', 'none']
}

/** 安全层纠偏：当前值不在可选项时回退首个（reality 兼容传输优先 reality，否则 tls）。 */
export function coerceExternalSecurity(
  protocol: string,
  network: string,
  security: string,
): string {
  const options = externalSecurityOptions(protocol, network)
  if (options.length === 0) return ''
  if (options.includes(security)) return security
  return options[0]
}

export interface ExternalExitFormState {
  protocol: string
  address: string
  port: string
  uuid: string
  username: string
  password: string
  method: string
  cipher: string
  flow: string
  encryption: string
  network: string
  security: string
  sni: string
  fingerprint: string
  publicKey: string
  shortId: string
  path: string
  host: string
  serviceName: string
  mode: string
}

export function defaultExternalExitForm(): ExternalExitFormState {
  return {
    protocol: 'http',
    address: '',
    port: '',
    uuid: '',
    username: '',
    password: '',
    method: '2022-blake3-aes-128-gcm',
    cipher: 'auto',
    flow: 'none',
    encryption: 'none',
    network: 'tcp',
    security: 'tls',
    sni: '',
    fingerprint: 'chrome',
    publicKey: '',
    shortId: '',
    path: '/',
    host: '',
    serviceName: 'grpc',
    mode: 'auto',
  }
}

/** 由已保存/已解析的外部出口规格回填表单（数字/空值统一成字符串）。 */
export function externalExitFormFromExit(exit?: ExternalExit | null): ExternalExitFormState {
  const base = defaultExternalExitForm()
  if (!exit) return base
  return {
    ...base,
    protocol: exit.protocol || base.protocol,
    address: exit.address ?? '',
    port: exit.port ? String(exit.port) : '',
    uuid: exit.uuid ?? '',
    username: exit.username ?? '',
    password: exit.password ?? '',
    method: exit.method || base.method,
    cipher: exit.cipher || base.cipher,
    flow: exit.flow || base.flow,
    encryption: exit.encryption || base.encryption,
    network: exit.network || base.network,
    security: exit.security || base.security,
    sni: exit.sni ?? '',
    fingerprint: exit.fingerprint || base.fingerprint,
    publicKey: exit.public_key ?? '',
    shortId: exit.short_id ?? '',
    path: exit.path || base.path,
    host: exit.host ?? '',
    serviceName: exit.service_name || base.serviceName,
    mode: exit.mode || base.mode,
  }
}

/** 服务器行 → 协议表单（编辑对话框回填；链接原文一并带回便于再次解析）。 */
export function externalExitFormFromServer(
  server: Pick<Server, 'external_exit'>,
): ExternalExitFormState {
  return externalExitFormFromExit(server.external_exit)
}

/** 解析结果 → 协议表单（点击「解析」后自动填写）。 */
export function externalExitFormFromPreview(
  preview: ParseExternalLinkResponse,
): ExternalExitFormState {
  return externalExitFormFromExit(preview.exit)
}

/** 协议表单 → 提交规格（与后端 normalizeExternalExit 的字段裁剪同构）。 */
export function externalExitFormPayload(form: ExternalExitFormState): ExternalExit {
  const protocol = form.protocol
  const exit: ExternalExit = {
    protocol: protocol as ExternalExit['protocol'],
    address: form.address.trim(),
    port: Number(form.port.trim() || 0),
  }
  const set = (key: keyof ExternalExit, value: string) => {
    const trimmed = value.trim()
    if (trimmed) (exit as unknown as Record<string, string>)[key] = trimmed
  }
  if (externalSupportsTransport(protocol)) {
    exit.network = form.network || 'tcp'
    exit.security = coerceExternalSecurity(protocol, exit.network, form.security) || 'none'
    if (exit.security === 'reality' || exit.security === 'tls') {
      if (form.sni.trim()) exit.sni = form.sni.trim()
      if (form.fingerprint) exit.fingerprint = form.fingerprint
    }
    if (exit.security === 'reality') {
      if (form.publicKey.trim()) exit.public_key = form.publicKey.trim()
      if (form.shortId.trim()) exit.short_id = form.shortId.trim()
    }
    if (exit.network === 'grpc') {
      exit.service_name = form.serviceName.trim() || 'grpc'
    }
    if (exit.network === 'ws' || exit.network === 'httpupgrade' || exit.network === 'xhttp') {
      exit.path = form.path.trim() || '/'
      if (form.host.trim()) exit.host = form.host.trim()
    }
    if (exit.network === 'xhttp') {
      exit.mode = form.mode || 'auto'
    }
  }
  switch (protocol) {
    case 'vless':
      set('uuid', form.uuid)
      if (form.flow && form.flow !== 'none' && exit.network === 'tcp') set('flow', form.flow)
      if (form.encryption && form.encryption !== 'none') set('encryption', form.encryption)
      break
    case 'vmess':
      set('uuid', form.uuid)
      if (form.cipher) exit.cipher = form.cipher
      break
    case 'trojan':
      set('password', form.password)
      break
    case 'shadowsocks':
      if (form.method) exit.method = form.method
      set('password', form.password)
      break
    case 'socks':
    case 'http':
      set('username', form.username)
      set('password', form.password)
      break
  }
  return exit
}

/** 协议表单校验（服务器表单没有必填项，仅校验已填写内容的合法性）。 */
export function externalExitFormError(form: ExternalExitFormState): string | null {
  const address = form.address.trim()
  if (address.includes(' ')) {
    return '地址不能包含空格'
  }
  const port = form.port.trim()
  if (port && (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535)) {
    return '端口须为 1-65535'
  }
  return null
}
