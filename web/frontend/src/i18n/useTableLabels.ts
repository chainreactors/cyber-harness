import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

// cstx tables take their chrome as a plain string map and substitute their own
// {n}/{start}/{end}/{total} placeholders after lookup, so single braces must
// reach them untouched — hence no interpolation arguments here.
const KEYS = [
  'selected', 'clear', 'filter', 'metadata', 'clearAll',
  'noMatch', 'noMatchHint', 'noTypeMatch', 'noTypeMatchHint', 'emptyHint',
  'clearSearch', 'clearFilters', 'rangeOf', 'perPage', 'emptyRows',
  'search', 'searchField',
  'exportXlsx', 'exportCsv', 'exportReport',
] as const

export function useTableLabels() {
  const { t } = useTranslation('table')
  return useMemo(
    () => Object.fromEntries(KEYS.map((key) => [key, t(key)])) as Record<string, string>,
    [t],
  )
}
