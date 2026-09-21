import { computed, onScopeDispose, shallowRef, ref, watch, type Ref } from 'vue'
import { getMatrix, type MonitorMatrixResponse, type MonitorMatrixRow } from '@/api/channelMonitorV2'

const CACHE_TTL_MS = 60_000

/** One user-scoped snapshot shared by the key page's group selectors, loaded on demand. */
export function useGroupChannelStatus(options: {
  enabled: Ref<boolean>
  active: Ref<boolean>
  userId: Ref<number | null>
  groupIds: Ref<number[]>
}) {
  const snapshot = shallowRef<MonitorMatrixResponse | null>(null)
  const loading = ref(false)
  const unavailable = ref(false)
  const groupIds = computed(() => [...new Set(options.groupIds.value)].sort((a, b) => a - b))
  const scope = computed(() => JSON.stringify([
    options.enabled.value, options.userId.value, groupIds.value,
  ]))
  let controller: AbortController | null = null
  let pending: Promise<void> | null = null
  let attemptedAt: number | null = null
  let disposed = false

  function reset() {
    controller?.abort()
    controller = null
    pending = null
    attemptedAt = null
    snapshot.value = null
    loading.value = false
    unavailable.value = false
  }

  function ensureLoaded(): Promise<void> {
    if (disposed || !options.enabled.value || !options.active.value ||
        options.userId.value === null || groupIds.value.length === 0) return Promise.resolve()
    if (pending) return pending
    if (attemptedAt !== null && Date.now() - attemptedAt < CACHE_TTL_MS) return Promise.resolve()

    const request = new AbortController()
    controller = request
    loading.value = true
    unavailable.value = false
    // Never present an expired green strip as a current result while refreshing.
    snapshot.value = null
    pending = (async () => {
      try {
        const result = await getMatrix({
          range: '90m', platforms: [], groupIds: groupIds.value, models: [],
        }, 'platform_group', false, request.signal)
        if (!request.signal.aborted) snapshot.value = result
      } catch {
        if (!request.signal.aborted) unavailable.value = true
      } finally {
        if (controller === request) {
          loading.value = false
          attemptedAt = Date.now()
          controller = null
          pending = null
        }
      }
    })()
    return pending
  }

  const rowsByGroup = computed(() => {
    const rows = new Map<number, MonitorMatrixRow[]>()
    for (const row of snapshot.value?.items ?? []) {
      if (row.group_id === undefined) continue
      const group = rows.get(row.group_id) ?? []
      group.push(row)
      rows.set(row.group_id, group)
    }
    return rows
  })

  function rowsForGroup(groupId: number, platform: string): MonitorMatrixRow[] {
    const rows = rowsByGroup.value.get(groupId) ?? []
    return platform === 'composite' ? rows : rows.filter(row => row.platform === platform)
  }

  watch(scope, reset, { flush: 'sync' })
  watch([options.active, scope], () => { void ensureLoaded() }, { immediate: true })
  onScopeDispose(() => {
    disposed = true
    reset()
  })

  return {
    coverage: computed(() => snapshot.value?.coverage ?? null),
    loading, unavailable, rowsForGroup, ensureLoaded,
  }
}
