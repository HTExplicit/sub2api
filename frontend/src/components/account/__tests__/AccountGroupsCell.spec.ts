import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AccountGroupsCell from '../AccountGroupsCell.vue'
import type { Group } from '@/types'

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

const groups = Array.from({ length: 5 }, (_, index) => ({
  id: index + 1,
  name: `group-${index + 1}`,
}) as Group)

let wrapper: VueWrapper | undefined
let table: HTMLTableElement | undefined

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  table?.remove()
  table = undefined
  vi.restoreAllMocks()
})

function getPanel(): HTMLElement {
  const panel = document.querySelector('#overlay-root .min-w-48')
  if (!(panel instanceof HTMLElement)) throw new Error('group popover not found')
  return panel
}

describe('AccountGroupsCell', () => {
  it('teleports its backdrop outside a transformed row and closes on its click', async () => {
    table = document.createElement('table')
    const row = table.insertRow()
    row.style.willChange = 'transform'
    const cell = row.insertCell()
    document.body.appendChild(table)
    wrapper = mount(AccountGroupsCell, {
      attachTo: cell,
      props: { groups },
      global: {
        stubs: {
          transition: false,
          GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
        },
      },
    })

    await wrapper.get('button').trigger('click')
    const overlay = document.getElementById('overlay-root')!
    const backdrop = document.querySelector<HTMLElement>('.fixed.inset-0.z-40')
    if (!backdrop) throw new Error('group backdrop not found')
    const panel = getPanel()
    expect(backdrop.parentElement).toBe(overlay)
    expect(panel.parentElement).toBe(overlay)
    expect(row.contains(backdrop)).toBe(false)

    backdrop.click()
    await wrapper.vm.$nextTick()
    expect(backdrop.isConnected).toBe(false)
    await vi.waitFor(() => expect(panel.isConnected).toBe(false))
  })

  it.each(['close', 'resetKey'] as const)(
    'positions a reused instance from its current trigger after %s',
    async closeBy => {
      wrapper = mount(AccountGroupsCell, {
        attachTo: document.body,
        props: { groups, resetKey: 1 },
        global: {
          stubs: {
            // Keep the leaving DOM so closing cannot silently clear its position.
            transition: false,
            GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
          },
        },
      })
      const owner = wrapper.vm
      const trigger = wrapper.get('button')
      let rect = new DOMRect(40, 80, 32, 24)
      const measure = vi.spyOn(trigger.element, 'getBoundingClientRect').mockImplementation(() => rect)

      await trigger.trigger('click')
      const firstPanel = getPanel()
      expect(firstPanel.style.top).toBe('112px')
      expect(firstPanel.style.left).toBe('40px')

      // The same mounted trigger may move when its physical row changes account.
      rect = new DOMRect(180, 240, 32, 24)
      if (closeBy === 'resetKey') {
        await wrapper.setProps({
          resetKey: 2,
          groups: groups.map(group => ({ ...group, name: `next-${group.name}` })),
        })
      } else {
        await trigger.trigger('click')
      }
      expect(firstPanel.isConnected).toBe(true)
      expect(firstPanel.style.top).toBe('112px')
      expect(firstPanel.style.left).toBe('40px')
      await vi.waitFor(() => expect(firstPanel.isConnected).toBe(false))

      await trigger.trigger('click')
      const nextPanel = getPanel()
      expect(wrapper.vm).toBe(owner)
      expect(wrapper.get('button').element).toBe(trigger.element)
      expect(nextPanel.style.top).toBe('272px')
      expect(nextPanel.style.left).toBe('180px')
      expect(measure).toHaveBeenCalledTimes(2)
      if (closeBy === 'resetKey') expect(nextPanel.textContent).toContain('next-group-5')
    },
  )
})
