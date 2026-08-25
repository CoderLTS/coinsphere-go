export const decimalPercent = (value: string) => {
  const negative = value.startsWith('-')
  const [integer, fraction = ''] = value.replace(/^-/, '').split('.')
  const scaled = BigInt(integer || '0') * 10_000n + BigInt(`${fraction}0000`.slice(0, 4))
  return `${negative ? '-' : ''}${scaled / 100n}.${String(scaled % 100n).padStart(2, '0')}`
}
