/**
 * GroupBadge contrast contracts:
 * - standard label uses translucent bg-black/10 + dark:bg-white/10 and inherits text color
 * - every opaque dark background is paired with an explicit dark text color
 * - name truncates (min-w-0 truncate) inside a max-w-full root
 */
import { mount, type VueWrapper } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { GroupPlatform, SubscriptionType } from '@/types'
import GroupBadge from '../GroupBadge.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null }),
}))

interface BadgeProps {
  name: string
  platform?: GroupPlatform
  subscriptionType?: SubscriptionType
  rateMultiplier?: number
  daysRemaining?: number | null
}

function mountBadge(props: BadgeProps): VueWrapper {
  return mount(GroupBadge, {
    props,
    global: {
      stubs: {
        PlatformIcon: true,
      },
    },
  })
}

/** The right-side label is the only span carrying the compact label chrome (peak span is never rendered here). */
function findLabel(wrapper: VueWrapper) {
  return wrapper.findAll('span').find((span) => {
    const classes = span.classes()
    return classes.includes('px-1.5') && classes.includes('py-0.5') && classes.includes('text-[10px]')
  })
}

const PLATFORMS: Array<GroupPlatform | undefined> = [
  undefined,
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'grok',
  'kimi',
  'zhipu',
  'deepseek',
  'minimax',
  'composite',
]
const SUBSCRIPTION_TYPES: SubscriptionType[] = ['standard', 'subscription']
const DAYS_REMAINING: Array<number | null> = [null, 2, 5, 30]

/** Opaque (non-alpha) dark background token, e.g. dark:bg-zinc-700 or bare dark:bg-white. */
const OPAQUE_DARK_BG = /^dark:bg-(?:[a-z]+-\d+|white|black)$/

describe('GroupBadge label contrast', () => {
  it('standard rate label uses translucent backgrounds and inherits text color', () => {
    const wrapper = mountBadge({ name: 'Example', platform: 'openai', rateMultiplier: 1 })

    const label = wrapper.findAll('span').find((span) => span.text().trim() === '1x')
    expect(label).toBeDefined()

    const classes = label!.classes()
    expect(classes).toContain('bg-white/50')
    expect(classes).toContain('dark:bg-white/10')
    expect(classes).not.toContain('dark:bg-white')
    expect(classes.some((cls) => cls.startsWith('dark:text-'))).toBe(false)
  })

  const matrix = PLATFORMS.flatMap((platform) =>
    SUBSCRIPTION_TYPES.flatMap((subscriptionType) =>
      DAYS_REMAINING.map((daysRemaining) => ({ platform, subscriptionType, daysRemaining }))
    )
  )

  it.each(matrix)(
    'pairs any opaque dark background with an explicit dark text color (%o)',
    ({ platform, subscriptionType, daysRemaining }) => {
      const wrapper = mountBadge({
        name: 'Example',
        platform,
        subscriptionType,
        daysRemaining,
        rateMultiplier: 1.5,
      })

      const label = findLabel(wrapper)
      expect(label).toBeDefined()

      const classes = label!.classes()
      const hasOpaqueDarkBg = classes.some((cls) => OPAQUE_DARK_BG.test(cls))
      const hasDarkText = classes.some((cls) => /^dark:text-/.test(cls))
      if (hasOpaqueDarkBg) {
        expect(hasDarkText).toBe(true)
      }
    }
  )
})

describe('GroupBadge overflow layout', () => {
  it('truncates the name inside a max-w-full root', () => {
    const wrapper = mountBadge({ name: 'A very long group name that must truncate', platform: 'openai' })

    expect(wrapper.classes()).toContain('max-w-full')

    // findAll('span') would list the root span first (its text equals the name when no label renders),
    // so query the inner name span directly.
    const nameSpan = wrapper.get('span > span.truncate')
    expect(nameSpan.text()).toBe('A very long group name that must truncate')
    expect(nameSpan.classes()).toContain('min-w-0')
    expect(nameSpan.classes()).toContain('truncate')
  })
})
