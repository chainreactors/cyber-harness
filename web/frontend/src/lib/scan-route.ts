export type RouteMode = 'none' | 'push' | 'replace'

export type RouteTarget =
  | { kind: 'root' }
  | { kind: 'session'; id: string }
  | { kind: 'scan'; id: string }

export function parseRoute(pathname: string): RouteTarget {
  const segments = pathname.split('/').filter(Boolean)
  if (segments.length >= 2 && segments[0] === 'sessions') {
    try {
      return { kind: 'session', id: decodeURIComponent(segments[1]) }
    } catch {
      return { kind: 'session', id: segments[1] }
    }
  }
  if (segments.length >= 2 && segments[0] === 'scans') {
    try {
      return { kind: 'scan', id: decodeURIComponent(segments[1]) }
    } catch {
      return { kind: 'scan', id: segments[1] }
    }
  }
  return { kind: 'root' }
}

export function scanIdFromPath(pathname: string) {
  const route = parseRoute(pathname)
  return route.kind === 'scan' ? route.id : null
}

export function sessionIdFromPath(pathname: string) {
  const route = parseRoute(pathname)
  return route.kind === 'session' ? route.id : null
}

export function isRootPath(pathname: string) {
  return pathname === '' || pathname === '/'
}

export function scanRoutePath(id: string) {
  return `/scans/${encodeURIComponent(id)}`
}

export function sessionRoutePath(id: string) {
  return `/sessions/${encodeURIComponent(id)}`
}

export function setScanRoute(id: string, mode: RouteMode) {
  setBrowserRoute(scanRoutePath(id), mode)
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
