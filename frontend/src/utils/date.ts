export const formatDateTime = (value?: string | number | Date | null) => {
  if (value === undefined || value === null || value === '') return '--'
  const normalizedValue =
    typeof value === 'string' &&
    /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?$/.test(value.trim())
      ? `${value.trim().replace(' ', 'T')}Z`
      : value
  const date = value instanceof Date ? value : new Date(normalizedValue)
  if (Number.isNaN(date.getTime())) return String(value)

  const utc8Date = new Date(date.getTime() + 8 * 60 * 60 * 1000)
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${utc8Date.getUTCFullYear()}-${pad(utc8Date.getUTCMonth() + 1)}-${pad(utc8Date.getUTCDate())} ${pad(utc8Date.getUTCHours())}:${pad(utc8Date.getUTCMinutes())}:${pad(utc8Date.getUTCSeconds())}`
}
