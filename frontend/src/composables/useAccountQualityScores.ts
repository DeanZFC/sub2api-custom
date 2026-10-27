import { onUnmounted, ref, watch, type Ref } from 'vue'
import { channelQualityAPI, type ChannelQualityScore } from '@/api/admin/channelQuality'

/** Fetch the visible page in bounded batches; a failed batch remains unknown. */
export function useAccountQualityScores(accounts: Ref<{ id: number }[]>, enabled: Ref<boolean>) {
  const scores = ref<Record<string, ChannelQualityScore>>({})
  let request = 0
  async function refresh() {
    const token = ++request
    const ids = [...new Set(accounts.value.map(account => account.id))]
    scores.value = {}
    if (!enabled.value || !ids.length) return
    const batches: number[][] = []
    for (let offset = 0; offset < ids.length; offset += 100) batches.push(ids.slice(offset, offset + 100))
    const results = await Promise.allSettled(batches.map(batch => channelQualityAPI.scores(batch)))
    if (token !== request) return
    const visible = new Set(ids.map(String)), next: Record<string, ChannelQualityScore> = {}
    for (const result of results) {
      if (result.status === 'fulfilled' && result.value && typeof result.value === 'object') {
        for (const [id, score] of Object.entries(result.value)) if (visible.has(id) && score) next[id] = score
      }
    }
    scores.value = next
  }
  watch([accounts, enabled], () => { void refresh() }, { immediate: true })
  onUnmounted(() => { request++ })
  return { scores, refresh }
}
