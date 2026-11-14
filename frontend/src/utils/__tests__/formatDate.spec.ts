import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const localeState = vi.hoisted(() => ({ value: 'en-GB' }))
vi.mock('@/i18n', () => ({ getLocale: () => localeState.value, i18n: {} }))

const NativeDateTimeFormat = Intl.DateTimeFormat
const dateTimeOptions: Intl.DateTimeFormatOptions = {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false
}
const minuteOptions: Intl.DateTimeFormatOptions = {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false
}

let format: typeof import('../format')

beforeEach(async () => {
  vi.resetModules()
  localeState.value = 'en-GB'
  format = await import('../format')
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('fixed date and time formats', () => {
  it('reuses the default formatter while formatting each date with seconds', () => {
    const first = new Date(2026, 6, 19, 20, 30, 45)
    const second = new Date(2026, 6, 20, 8, 15, 12)
    const expected = new NativeDateTimeFormat('en-GB', dateTimeOptions)
    const constructor = vi.spyOn(Intl, 'DateTimeFormat')

    expect(format.formatDateTime(first)).toBe(expected.format(first))
    expect(format.formatDate(second)).toBe(expected.format(second))
    expect(constructor).toHaveBeenCalledTimes(1)
  })

  it('reuses the minute formatter and preserves the account expiry locale override', () => {
    const first = new Date(2026, 6, 19, 20, 30, 45)
    const second = new Date(2026, 6, 20, 8, 15, 12)
    const expected = new NativeDateTimeFormat('sv-SE', minuteOptions)
    const constructor = vi.spyOn(Intl, 'DateTimeFormat')

    expect(format.formatDateTimeToMinute(first, 'sv-SE')).toBe(expected.format(first))
    expect(format.formatDateTimeToMinute(second, 'sv-SE')).toBe(expected.format(second))
    expect(constructor).toHaveBeenCalledTimes(1)
  })

  it('replaces each fixed formatter when the current locale changes', () => {
    const value = new Date(2026, 6, 19, 20, 30, 45)
    const constructor = vi.spyOn(Intl, 'DateTimeFormat')

    for (const locale of ['en-GB', 'zh', 'en-GB']) {
      localeState.value = locale
      expect(format.formatDateTime(value)).toBe(
        new NativeDateTimeFormat(locale, dateTimeOptions).format(value)
      )
      expect(format.formatDateTimeToMinute(value)).toBe(
        new NativeDateTimeFormat(locale, minuteOptions).format(value)
      )
    }

    expect(constructor).toHaveBeenCalledTimes(6)
  })

  it('uses custom options directly, including changes to the same options object', () => {
    const value = new Date('2026-07-19T20:30:45Z')
    const options: Intl.DateTimeFormatOptions = { hour: '2-digit', timeZone: 'UTC' }
    const constructor = vi.spyOn(Intl, 'DateTimeFormat')

    expect(format.formatDateTime(value, options, 'en-GB')).toBe(
      new NativeDateTimeFormat('en-GB', options).format(value)
    )
    options.timeZone = 'Asia/Shanghai'
    expect(format.formatDateTime(value, options, 'en-GB')).toBe(
      new NativeDateTimeFormat('en-GB', options).format(value)
    )
    expect(constructor).toHaveBeenCalledTimes(2)
  })

  it('keeps empty and invalid dates ahead of locale validation', () => {
    const constructor = vi.spyOn(Intl, 'DateTimeFormat')

    expect(format.formatDateTime(null, undefined, 'invalid_locale')).toBe('')
    expect(format.formatDateTime(new Date('invalid'), undefined, 'invalid_locale')).toBe('')
    expect(format.formatDateTimeToMinute(undefined, 'invalid_locale')).toBe('')
    expect(constructor).not.toHaveBeenCalled()
    expect(() => format.formatDateTime(new Date(), undefined, 'invalid_locale')).toThrow(RangeError)
    expect(() => format.formatDateTimeToMinute(new Date(), 'invalid_locale')).toThrow(RangeError)
  })
})
