export type OS = 'linux' | 'darwin' | 'windows'
export type Arch = 'amd64' | 'arm64'
export type DownloadSource = 'global' | 'china'
export interface NodeBootstrapOptions {
  os: OS
  arch: Arch
  // The Hub manifest may also supply custom installable node IDs.
  distribution: string
  source: DownloadSource
  version?: string
  serverURL: string
  accessToken: string
  nodeName: string
}

export function nodeDownloadURL(options: Pick<NodeBootstrapOptions, 'os' | 'arch' | 'distribution' | 'source' | 'version'>): string {
  const version = options.version?.trim()
  const tag = !version || version === 'dev' ? 'latest' : version.startsWith('v') ? version : `v${version}`
  const base = tag === 'latest'
    ? 'https://github.com/chainreactors/cyber-harness/releases/latest/download'
    : `https://github.com/chainreactors/cyber-harness/releases/download/${tag}`
  const url = `${base}/${options.distribution}_${options.os}_${options.arch}.zip`
  return options.source === 'china' ? `https://ghfast.top/${url}` : url
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

function powershellQuote(value: string): string {
  return `'${value.replace(/'/g, `''`)}'`
}

// The caller supplies an absolute Hub URL. Credentials stay in the command
// shown to the authenticated user and never enter the public download URL.
export function nodeCommands(options: NodeBootstrapOptions): { install: string; connect: string } {
  const { os, distribution, nodeName } = options
  const url = new URL(options.serverURL)
  url.username = options.accessToken
  url.password = ''
  const endpoint = url.toString().replace(/\/$/, '')
  const download = nodeDownloadURL(options)
  const command = distribution === 'cyber-scan' ? ' agent' : ''
  const quote = os === 'windows' ? powershellQuote : shellQuote
  const args = `--server-url ${quote(endpoint)} --node-name ${quote(nodeName)}`
  if (os === 'windows') {
    const binary = `${distribution}.exe`
    // Run directly in PowerShell; nesting another interpreter would expand
    // its variables in the parent shell before the child can use them.
    // Use the two-argument .NET API supported by Windows PowerShell 5.1;
    // installed Expand-Archive modules can require a newer .NET overload.
    const install = `& { $ErrorActionPreference='Stop'; $tmp=Join-Path ([IO.Path]::GetTempPath()) ('cyber-node-' + [guid]::NewGuid()); New-Item -ItemType Directory -Path $tmp | Out-Null; try { Invoke-WebRequest -UseBasicParsing ${quote(download)} -OutFile (Join-Path $tmp 'node.zip'); Add-Type -AssemblyName System.IO.Compression.FileSystem; [IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $tmp 'node.zip'), $tmp); & (Join-Path $tmp ${quote(binary)})${command} ${args}; if ($LASTEXITCODE -ne 0) { throw ('Node exited with code ' + $LASTEXITCODE) } } finally { if ((Split-Path -Parent $tmp) -eq ([IO.Path]::GetTempPath().TrimEnd([IO.Path]::DirectorySeparatorChar))) { Remove-Item -LiteralPath $tmp -Recurse -Force } } }`
    return { install, connect: `.\\${binary}${command} ${args}` }
  }
  const binary = `"$tmp/${distribution}"`
  // A subshell scopes the trap and temporary variable to this launch, so
  // cleanup runs when the node exits instead of waiting for the terminal.
  const install = `(tmp="$(mktemp -d)" && trap 'rm -rf "$tmp"' EXIT && curl -fsSL ${quote(download)} -o "$tmp/node.zip" && unzip -qo "$tmp/node.zip" ${quote(distribution)} -d "$tmp" && chmod +x ${binary} && ${binary}${command} ${args})`
  return { install, connect: `./${distribution}${command} ${args}` }
}
