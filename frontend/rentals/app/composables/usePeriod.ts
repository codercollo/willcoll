// The billing period lives in the URL (?period=YYYY-MM) so it survives a
// refresh and is shared by the Rent, Water, Garbage and Reports tabs.
export function usePeriod() {
  const route = useRoute()
  return computed({
    get: () => (isPeriod(String(route.query.period ?? '')) ? String(route.query.period) : currentPeriod()),
    set: (v: string) => { navigateTo({ query: { ...route.query, period: v } }, { replace: true }) },
  })
}
