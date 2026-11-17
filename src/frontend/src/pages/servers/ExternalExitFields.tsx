import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Button } from '@/components/ui/button'

import {
  EXTERNAL_ENCRYPTIONS,
  EXTERNAL_FINGERPRINTS,
  EXTERNAL_FLOWS,
  EXTERNAL_NETWORKS,
  EXTERNAL_PROTOCOLS,
  EXTERNAL_PROTOCOL_LABELS,
  EXTERNAL_SS_METHODS,
  EXTERNAL_VMESS_CIPHERS,
  EXTERNAL_XHTTP_MODES,
  coerceExternalSecurity,
  externalSecurityOptions,
  externalSupportsTransport,
  type ExternalExitFormState,
} from './external-exit-form'

/**
 * 未托管外部直链节点的协议表单（服务器创建/编辑共用）。
 * 分享链接只是便捷入口：点击「解析」把协议/地址/端口/凭据/传输自动填进下面的
 * 输入框；保存的是协议参数本身，因此表单没有必填项（不完整时链路页会提示补全）。
 */
export function ExternalExitFields({
  idPrefix = 'external',
  link,
  parsing,
  parseError,
  exit,
  onLinkChange,
  onParse,
  onExitChange,
}: {
  idPrefix?: string
  link: string
  parsing: boolean
  parseError: string
  exit: ExternalExitFormState
  onLinkChange: (link: string) => void
  onParse: () => void
  onExitChange: (patch: Partial<ExternalExitFormState>) => void
}) {
  const supportsTransport = externalSupportsTransport(exit.protocol)
  const securityOptions = externalSecurityOptions(exit.protocol, exit.network)
  const plainNetwork = exit.network === 'ws' || exit.network === 'httpupgrade'
  const id = (name: string) => `${idPrefix}-${name}`

  const changeProtocol = (protocol: string) => {
    onExitChange({
      protocol,
      security: coerceExternalSecurity(protocol, exit.network, exit.security),
    })
  }
  const changeNetwork = (network: string) => {
    onExitChange({
      network,
      security: coerceExternalSecurity(exit.protocol, network, exit.security),
    })
  }

  return (
    <div className="space-y-3">
      <div className="space-y-2">
        <Label htmlFor={id('link')}>分享链接（可选）</Label>
        <textarea
          id={id('link')}
          className="sv-textarea"
          rows={2}
          value={link}
          onChange={(event) => onLinkChange(event.target.value)}
          placeholder="vless://uuid@host:443?type=tcp&security=reality&pbk=...&sid=...#名称"
          spellCheck={false}
        />
        <div className="flex items-start justify-between gap-2">
          <p className="text-xs text-muted-foreground">
            粘贴分享链接后点「解析」自动填写下方协议参数；也可以直接手动填写。外部节点没有 Agent，
            只作为链路的最后一跳出口。
          </p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="shrink-0"
            disabled={parsing || !link.trim()}
            onClick={onParse}
          >
            {parsing ? '解析中…' : '解析'}
          </Button>
        </div>
        {parseError ? <p className="text-sm text-destructive">{parseError}</p> : null}
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-2">
          <Label>协议</Label>
          <Select value={exit.protocol} onValueChange={(value) => value && changeProtocol(value)}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {EXTERNAL_PROTOCOLS.map((protocol) => (
                <SelectItem key={protocol} value={protocol}>
                  {EXTERNAL_PROTOCOL_LABELS[protocol] ?? protocol}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label htmlFor={id('port')}>端口</Label>
          <Input
            id={id('port')}
            inputMode="numeric"
            value={exit.port}
            onChange={(event) => onExitChange({ port: event.target.value })}
            placeholder="如 443"
          />
        </div>
      </div>

      <div className="space-y-2">
        <Label htmlFor={id('address')}>地址（域名或 IP）</Label>
        <Input
          id={id('address')}
          value={exit.address}
          onChange={(event) => onExitChange({ address: event.target.value })}
          placeholder="如 gw.example.com"
          spellCheck={false}
        />
      </div>

      {exit.protocol === 'vless' ? (
        <>
          <div className="space-y-2">
            <Label htmlFor={id('uuid')}>UUID</Label>
            <Input
              id={id('uuid')}
              value={exit.uuid}
              onChange={(event) => onExitChange({ uuid: event.target.value })}
              placeholder="客户端 UUID"
              spellCheck={false}
            />
          </div>
          {supportsTransport ? (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label>传输（network）</Label>
                <Select
                  value={exit.network}
                  onValueChange={(value) => value && changeNetwork(value)}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {EXTERNAL_NETWORKS.map((network) => (
                      <SelectItem key={network} value={network}>
                        {network}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label>安全层（security）</Label>
                <Select
                  value={exit.security}
                  onValueChange={(value) => value && onExitChange({ security: value })}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {securityOptions.map((security) => (
                      <SelectItem key={security} value={security}>
                        {security}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
          ) : null}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label>flow</Label>
              <Select
                value={exit.flow}
                onValueChange={(value) => value && onExitChange({ flow: value })}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EXTERNAL_FLOWS.map((flow) => (
                    <SelectItem key={flow.value} value={flow.value}>
                      {flow.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>VLESS Encryption</Label>
              <Select
                value={exit.encryption}
                onValueChange={(value) => value && onExitChange({ encryption: value })}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EXTERNAL_ENCRYPTIONS.map((enc) => (
                    <SelectItem key={enc.value} value={enc.value}>
                      {enc.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
        </>
      ) : null}

      {exit.protocol === 'vmess' ? (
        <>
          <div className="space-y-2">
            <Label htmlFor={id('vmess-uuid')}>UUID</Label>
            <Input
              id={id('vmess-uuid')}
              value={exit.uuid}
              onChange={(event) => onExitChange({ uuid: event.target.value })}
              placeholder="客户端 UUID"
              spellCheck={false}
            />
          </div>
          <div className="space-y-2">
            <Label>加密方式（cipher）</Label>
            <Select
              value={exit.cipher}
              onValueChange={(value) => value && onExitChange({ cipher: value })}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EXTERNAL_VMESS_CIPHERS.map((cipher) => (
                  <SelectItem key={cipher} value={cipher}>
                    {cipher}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </>
      ) : null}

      {exit.protocol === 'trojan' ? (
        <div className="space-y-2">
          <Label htmlFor={id('trojan-password')}>密码</Label>
          <Input
            id={id('trojan-password')}
            value={exit.password}
            onChange={(event) => onExitChange({ password: event.target.value })}
            placeholder="Trojan 密码"
          />
        </div>
      ) : null}

      {exit.protocol === 'shadowsocks' ? (
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-2">
            <Label>加密方式</Label>
            <Select
              value={exit.method}
              onValueChange={(value) => value && onExitChange({ method: value })}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EXTERNAL_SS_METHODS.map((method) => (
                  <SelectItem key={method} value={method}>
                    {method}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor={id('ss-password')}>密码</Label>
            <Input
              id={id('ss-password')}
              value={exit.password}
              onChange={(event) => onExitChange({ password: event.target.value })}
              placeholder="Shadowsocks 密码"
            />
          </div>
        </div>
      ) : null}

      {exit.protocol === 'socks' || exit.protocol === 'http' ? (
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-2">
            <Label htmlFor={id('username')}>用户名（可选）</Label>
            <Input
              id={id('username')}
              value={exit.username}
              onChange={(event) => onExitChange({ username: event.target.value })}
              placeholder={exit.protocol === 'http' ? 'Basic 认证用户名' : 'SOCKS5 用户名'}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={id('password')}>密码（可选）</Label>
            <Input
              id={id('password')}
              type="password"
              value={exit.password}
              onChange={(event) => onExitChange({ password: event.target.value })}
              placeholder="密码"
            />
          </div>
        </div>
      ) : null}

      {supportsTransport ? (
        <>
          {exit.security === 'reality' ? (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label htmlFor={id('pbk')}>Reality 公钥（pbk）</Label>
                <Input
                  id={id('pbk')}
                  value={exit.publicKey}
                  onChange={(event) => onExitChange({ publicKey: event.target.value })}
                  spellCheck={false}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor={id('sid')}>shortId</Label>
                <Input
                  id={id('sid')}
                  value={exit.shortId}
                  onChange={(event) => onExitChange({ shortId: event.target.value })}
                  spellCheck={false}
                />
              </div>
            </div>
          ) : null}
          {exit.security === 'reality' || exit.security === 'tls' ? (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label htmlFor={id('sni')}>SNI / serverName</Label>
                <Input
                  id={id('sni')}
                  value={exit.sni}
                  onChange={(event) => onExitChange({ sni: event.target.value })}
                  placeholder="如 www.example.com"
                  spellCheck={false}
                />
              </div>
              <div className="space-y-2">
                <Label>uTLS 指纹</Label>
                <Select
                  value={exit.fingerprint}
                  onValueChange={(value) => value && onExitChange({ fingerprint: value })}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {EXTERNAL_FINGERPRINTS.map((fingerprint) => (
                      <SelectItem key={fingerprint} value={fingerprint}>
                        {fingerprint}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
          ) : null}
          {exit.network === 'grpc' ? (
            <div className="space-y-2">
              <Label htmlFor={id('service')}>gRPC serviceName</Label>
              <Input
                id={id('service')}
                value={exit.serviceName}
                onChange={(event) => onExitChange({ serviceName: event.target.value })}
                placeholder="grpc"
              />
            </div>
          ) : null}
          {exit.network === 'ws' || exit.network === 'httpupgrade' || exit.network === 'xhttp' ? (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label htmlFor={id('path')}>path</Label>
                <Input
                  id={id('path')}
                  value={exit.path}
                  onChange={(event) => onExitChange({ path: event.target.value })}
                  placeholder="/"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor={id('host')}>Host（可选）</Label>
                <Input
                  id={id('host')}
                  value={exit.host}
                  onChange={(event) => onExitChange({ host: event.target.value })}
                  placeholder="留空不设置"
                />
              </div>
            </div>
          ) : null}
          {exit.network === 'xhttp' ? (
            <div className="space-y-2">
              <Label>xhttp mode</Label>
              <Select
                value={exit.mode}
                onValueChange={(value) => value && onExitChange({ mode: value })}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EXTERNAL_XHTTP_MODES.map((mode) => (
                    <SelectItem key={mode} value={mode}>
                      {mode}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ) : null}
          {plainNetwork ? (
            <p className="text-xs text-muted-foreground">
              ws/httpupgrade 使用 TLS 或明文（none）安全层；Reality 仅支持 tcp/grpc/xhttp。
            </p>
          ) : null}
        </>
      ) : null}
    </div>
  )
}
