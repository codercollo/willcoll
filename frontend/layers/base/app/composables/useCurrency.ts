export function useCurrency() {
  return {
    format: formatMoney,
    isZero: isZeroMoney,
    isNegative: isNegativeMoney,
    prefixed: (v: string | null | undefined) => `Ksh ${formatMoney(v)}`,
  }
}
