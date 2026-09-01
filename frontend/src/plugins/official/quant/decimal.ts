export const decimalPercent = (value: string) => {
  const negative = value.startsWith('-')
  const [integer, fraction = ''] = value.replace(/^-/, '').split('.')
  const digits = `${fraction}00000`
  const scaled =
    BigInt(integer || '0') * 10_000n + BigInt(digits.slice(0, 4)) + (digits[4] >= '5' ? 1n : 0n)
  return `${negative ? '-' : ''}${scaled / 100n}.${String(scaled % 100n).padStart(2, '0')}`
}

export const decimalFixed = (value: string, places = 2) => {
  const negative = value.startsWith('-')
  const [integer, fraction = ''] = value.replace(/^-/, '').split('.')
  const digits = `${fraction}${'0'.repeat(places + 1)}`
  const scale = 10n ** BigInt(places)
  const rounded =
    BigInt(integer || '0') * scale +
    BigInt(digits.slice(0, places) || '0') +
    (digits[places] >= '5' ? 1n : 0n)
  const sign = negative && rounded !== 0n ? '-' : ''
  if (!places) return `${sign}${rounded}`
  return `${sign}${rounded / scale}.${String(rounded % scale).padStart(places, '0')}`
}
