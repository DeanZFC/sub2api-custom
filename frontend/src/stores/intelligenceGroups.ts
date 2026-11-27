import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { intelligenceGroups, type IntelligenceGroup } from '@/api/intelligence'
import { useAuthStore } from './auth'

export const useIntelligenceGroupsStore = defineStore('intelligence-groups', () => {
  const auth = useAuthStore()
  const groups = ref<IntelligenceGroup[]>([])
  const refreshed = ref(0)
  const failed = ref(false)
  const loading = ref(false)
  let controller: AbortController | undefined
  let pending: Promise<void> | undefined
  const userId = computed(() => auth.user?.id)
  watch(
    userId,
    () => {
      controller?.abort()
      pending = undefined
      groups.value = []
      refreshed.value = 0
      failed.value = false
      loading.value = false
    },
    { flush: 'sync' },
  )
  function load(force = false): Promise<void> {
    if (!userId.value) return Promise.resolve()
    if (pending) return pending
    if (!force && Date.now() - refreshed.value < 60_000) return Promise.resolve()
    const request = new AbortController()
    controller = request
    loading.value = true
    pending = intelligenceGroups(request.signal)
      .then((items) => {
        if (request.signal.aborted) return
        groups.value = items
        refreshed.value = Date.now()
        failed.value = false
      })
      .catch(() => {
        if (request.signal.aborted) return
        groups.value = []
        failed.value = true
      })
      .finally(() => {
        if (!request.signal.aborted) {
          pending = undefined
          loading.value = false
        }
      })
    return pending
  }
  return { groups, refreshed, failed, loading, load }
})
