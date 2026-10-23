<template>
  <div class="monitor-status-strip flex min-w-0 items-end gap-1" :class="compact ? 'h-6' : 'h-9'" :aria-label="label">
    <button
      v-for="(slot, index) in slots"
      :key="index"
      type="button"
      class="flex h-full min-w-0 flex-1 cursor-pointer items-end rounded-[3px] border-0 bg-transparent p-0 outline-offset-2 transition-opacity hover:opacity-75 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
      :aria-label="slot.label"
      :aria-describedby="activeIndex === index ? tooltipId : undefined"
      :aria-pressed="activeIndex === index && pinned"
      :data-state="slot.state"
      @pointerenter="onEnter($event, index)"
      @pointerleave="onLeave"
      @focus="openTooltip($event, index)"
      @blur="onBlur"
      @click.stop="toggleTooltip($event, index)"
    >
      <span
        aria-hidden="true"
        class="pointer-events-none block w-full rounded-[3px]"
        :class="[slot.colorClass || colors[slot.state], slot.state === 'unknown' ? 'h-1.5' : 'h-full']"
      />
    </button>
  </div>
  <Teleport to="body">
    <div
      v-if="activeSlot"
      :id="tooltipId"
      ref="tooltip"
      role="tooltip"
      tabindex="-1"
      class="fixed z-[99999] w-max max-w-[min(20rem,calc(100vw-24px))] overflow-y-auto whitespace-pre-line break-words rounded-lg bg-gray-900 p-3 text-xs leading-5 text-white shadow-xl ring-1 ring-white/10 dark:bg-gray-800"
      :style="tooltipStyle"
      @pointerleave="onTooltipLeave"
      @blur="onBlur"
    >
      <div>{{ activeSlot.label }}</div>
      <button
        v-if="activeSlot.actionLabel"
        type="button"
        class="mt-2 block text-primary-300 underline hover:text-primary-200"
        @click.stop="selectSlot"
      >{{ activeSlot.actionLabel }}</button>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import type { HealthState } from '@/api/channelMonitorV2'
import './monitorHealthColors.css'

const props = defineProps<{ slots: Array<{ state: HealthState; label: string; colorClass?: string; actionLabel?: string }>; label: string; compact?: boolean }>()
const emit = defineEmits<{ select: [index: number] }>()
const tooltipId = `monitor-status-${useId()}`
const tooltip = ref<HTMLElement | null>(null)
const activeIndex = ref<number | null>(null)
const activeSlot = computed(() => activeIndex.value == null ? null : props.slots[activeIndex.value])
const pinned = ref(false)
const tooltipStyle = ref({ left: '0px', top: '0px', maxHeight: 'calc(100dvh - 24px)' })
let trigger: HTMLElement | null = null

function isInside(element: HTMLElement | null, target: EventTarget | null) {
  return target instanceof Node && !!element?.contains(target)
}

function closeTooltip() {
  activeIndex.value = null
  pinned.value = false
  trigger = null
}

function selectSlot() {
  if (activeIndex.value == null || !activeSlot.value?.actionLabel) return
  emit('select', activeIndex.value)
  closeTooltip()
}

function openTooltip(event: Event, index: number) {
  const element = event.currentTarget
  if (!(element instanceof HTMLElement)) return
  if (activeIndex.value !== index) pinned.value = false
  trigger = element
  activeIndex.value = index
  document.dispatchEvent(new CustomEvent('monitor-status-tooltip-open', { detail: tooltipId }))
  void nextTick(updatePosition)
}

function updatePosition() {
  if (!trigger || !tooltip.value) return
  const anchor = trigger.getBoundingClientRect()
  const panel = tooltip.value.getBoundingClientRect()
  const margin = 12
  const gap = 8
  const left = Math.max(margin, Math.min(anchor.left + anchor.width / 2 - panel.width / 2, window.innerWidth - panel.width - margin))
  const top = anchor.top - panel.height - gap >= margin
    ? anchor.top - panel.height - gap
    : Math.max(margin, Math.min(anchor.bottom + gap, window.innerHeight - panel.height - margin))
  tooltipStyle.value = { ...tooltipStyle.value, left: `${left}px`, top: `${top}px` }
}

function onEnter(event: PointerEvent, index: number) {
  // Touch browsers can synthesize hover before click; only a click pins details.
  if (event.pointerType === 'touch' || pinned.value) return
  openTooltip(event, index)
}

function onLeave(event: PointerEvent) {
  if (!pinned.value && !isInside(tooltip.value, event.relatedTarget)) closeTooltip()
}

function onTooltipLeave(event: PointerEvent) {
  if (!pinned.value && !isInside(trigger, event.relatedTarget)) closeTooltip()
}

function onBlur(event: FocusEvent) {
  if (!isInside(tooltip.value, event.relatedTarget) && !isInside(trigger, event.relatedTarget)) closeTooltip()
}

function toggleTooltip(event: MouseEvent, index: number) {
  if (activeIndex.value === index && pinned.value) {
    closeTooltip()
    return
  }
  // Focus/hover may have opened the tooltip before the first click. Keep it open.
  openTooltip(event, index)
  pinned.value = true
}

// Auto-refresh can move requests to another slot: never leave stale details pinned.
watch(() => props.slots, closeTooltip)
watch(activeIndex, (index, _, onCleanup) => {
  if (index == null) return
  const onOutside = (event: Event) => {
    if (!isInside(trigger, event.target) && !isInside(tooltip.value, event.target)) closeTooltip()
  }
  const onKeydown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') closeTooltip()
  }
  const onScroll = (event: Event) => {
    if (!isInside(tooltip.value, event.target)) closeTooltip()
  }
  const onOtherTooltip = (event: Event) => {
    if ((event as CustomEvent<string>).detail !== tooltipId) closeTooltip()
  }
  document.addEventListener('pointerdown', onOutside, true)
  document.addEventListener('click', onOutside, true)
  document.addEventListener('keydown', onKeydown)
  document.addEventListener('monitor-status-tooltip-open', onOtherTooltip)
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', closeTooltip)
  onCleanup(() => {
    document.removeEventListener('pointerdown', onOutside, true)
    document.removeEventListener('click', onOutside, true)
    document.removeEventListener('keydown', onKeydown)
    document.removeEventListener('monitor-status-tooltip-open', onOtherTooltip)
    window.removeEventListener('scroll', onScroll, true)
    window.removeEventListener('resize', closeTooltip)
  })
})

const colors: Record<HealthState, string> = {
  healthy: 'bg-emerald-400 dark:bg-emerald-500',
  warning: 'bg-amber-400 dark:bg-amber-500',
  critical: 'bg-rose-400 dark:bg-rose-500',
  unknown: 'bg-gray-300 dark:bg-dark-600',
}
</script>
