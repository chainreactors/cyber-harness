// Browser route helpers for the SPA's addressable views.
export type RouteMode = 'none' | 'push' | 'replace'

// Sessions are the only addressable view: scanning is inlined into the chat, so
// the retired /scans/<id> route has no target and must not parse as one.
export type RouteTarget =
  | { kind: 'root' }
  | { kind: 'session'; id: string }

export function parseRoute(pathname: string): RouteTarget {
  const segments = pathname.split('/').filter(Boolean)
  if (segments.length >= 2 && segments[0] === 'sessions') {
    try {
      return { kind: 'session', id: decodeURIComponent(segments[1]) }
    } catch {
      return { kind: 'session', id: segments[1] }
    }
  }
  return { kind: 'root' }
}

export function isRootPath(pathname: string) {
  return pathname === '' || pathname === '/'
}

export function sessionRoutePath(id: string) {
  return `/sessions/${encodeURIComponent(id)}`
}

export function setSessionRoute(id: string, mode: RouteMode) {
  setBrowserRoute(sessionRoutePath(id), mode)
}

function setBrowserRoute(path: string, mode: RouteMode) {
  if (mode === 'none') {
    return
  }
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`
  if (current === path) {
    return
  }
  if (mode === 'replace') {
    window.history.replaceState({}, '', path)
  } else {
    window.history.pushState({}, '', path)
  }
}
