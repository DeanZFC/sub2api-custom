import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import MonitorStatusStrip from '../MonitorStatusStrip.vue'

const slots = [
  { state: 'unknown' as const, label: '暂无请求' },
  { state: 'critical' as const, label: 'gpt-test · 请求失败\n首字：-\n内部错误' },
]
const wrappers: ReturnType<typeof mount>[] = []
function render() {
  const wrapper = mount(MonitorStatusStrip, { attachTo: document.body, props: { slots, label: '近 20 次请求状态', compact: true } })
  wrappers.push(wrapper)
  return wrapper
}
const tooltip = () => document.body.querySelector<HTMLElement>('[role=tooltip]')

afterEach(() => {
  wrappers.forEach(wrapper => wrapper.unmount())
  wrappers.length = 0
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('MonitorStatusStrip details', () => {
  it('shows complete details immediately on hover without a native title', async () => {
    const wrapper = render()
    const trigger = wrapper.findAll('button')[1]
    expect(trigger.attributes('title')).toBeUndefined()
    expect(tooltip()).toBeNull()
    await trigger.trigger('pointerenter', { pointerType: 'mouse' })
    expect(tooltip()?.textContent).toBe(slots[1].label)
    expect(trigger.attributes('aria-describedby')).toBe(tooltip()?.id)
    await trigger.trigger('pointerleave')
    expect(tooltip()).toBeNull()
  })

  it('opens on a touch click and stays visible after pointer leave until tapped again', async () => {
    const trigger = render().findAll('button')[1]
    await trigger.trigger('pointerenter', { pointerType: 'touch' })
    expect(tooltip()).toBeNull()
    // Browsers focus buttons before firing their click event.
    await trigger.trigger('focus')
    await trigger.trigger('click')
    await trigger.trigger('pointerleave', { pointerType: 'touch' })
    expect(tooltip()?.textContent).toBe(slots[1].label)
    expect(trigger.attributes('aria-pressed')).toBe('true')
    await trigger.trigger('click')
    expect(tooltip()).toBeNull()
    expect(trigger.attributes('aria-pressed')).toBe('false')
  })

  it('pins an already hovered tooltip on the first click and switches to another slot', async () => {
    const triggers = render().findAll('button')
    await triggers[1].trigger('pointerenter', { pointerType: 'mouse' })
    await triggers[1].trigger('click')
    await triggers[1].trigger('pointerleave')
    expect(tooltip()?.textContent).toBe(slots[1].label)
    await triggers[0].trigger('click')
    expect(tooltip()?.textContent).toBe(slots[0].label)
    expect(document.querySelectorAll('[role=tooltip]')).toHaveLength(1)
  })

  it('dismisses on outside touch, Escape, or keyboard focus moving away', async () => {
    const trigger = render().findAll('button')[1]
    await trigger.trigger('click')
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()
    expect(tooltip()).toBeNull()
    await trigger.trigger('focus')
    expect(tooltip()?.textContent).toBe(slots[1].label)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(tooltip()).toBeNull()
    await trigger.trigger('focus')
    await trigger.trigger('blur')
    expect(tooltip()).toBeNull()
  })

  it('allows reading and selecting pinned details before toggling them closed', async () => {
    const trigger = render().findAll('button')[1]
    await trigger.trigger('click')
    await trigger.trigger('blur', { relatedTarget: tooltip() })
    tooltip()?.focus()
    await nextTick()
    expect(tooltip()?.textContent).toBe(slots[1].label)
    // Refocusing the original trigger must not turn the second click into an open.
    tooltip()?.dispatchEvent(new FocusEvent('blur', { relatedTarget: trigger.element }))
    await trigger.trigger('focus')
    await trigger.trigger('click')
    expect(tooltip()).toBeNull()
  })

  it('keeps only one strip tooltip open and removes it when unmounted', async () => {
    const first = render()
    const second = render()
    await first.findAll('button')[1].trigger('click')
    await second.findAll('button')[0].trigger('click')
    expect(document.querySelectorAll('[role=tooltip]')).toHaveLength(1)
    expect(tooltip()?.textContent).toBe(slots[0].label)
    expect(first.findAll('button')[1].attributes('aria-pressed')).toBe('false')
    second.unmount()
    expect(tooltip()).toBeNull()
  })

  it('dismisses details on refresh and page scrolling so they cannot point to stale data', async () => {
    const wrapper = render()
    const trigger = wrapper.findAll('button')[1]
    await trigger.trigger('click')
    await wrapper.setProps({ slots: slots.map(slot => ({ ...slot, label: '刷新后的请求' })) })
    expect(tooltip()).toBeNull()
    await trigger.trigger('click')
    tooltip()?.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(tooltip()).not.toBeNull()
    window.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(tooltip()).toBeNull()
  })

  it.each([
    [0, 4, 12, 36],
    [365, 500, 63, 312],
  ])('keeps the popup inside a narrow viewport at x=%s, y=%s', async (left, top, expectedLeft, expectedTop) => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(375)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(667)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'tooltip'
        ? { width: 300, height: 180 } as DOMRect
        : { left, top, bottom: top + 24, width: 10, height: 24 } as DOMRect
    })
    await render().findAll('button')[1].trigger('click')
    await nextTick()
    expect(tooltip()?.style.left).toBe(`${expectedLeft}px`)
    expect(tooltip()?.style.top).toBe(`${expectedTop}px`)
  })
})
