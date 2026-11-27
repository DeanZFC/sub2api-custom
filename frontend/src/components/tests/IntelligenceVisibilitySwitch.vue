<template>
  <div class="space-y-1">
    <div class="flex items-center gap-3">
      <label for="intelligence-visibility" class="cursor-pointer text-sm font-medium">{{ tr('publicVisibility') }}</label>
      <Toggle id="intelligence-visibility" class="disabled:cursor-default disabled:opacity-60" :model-value="enabled" :disabled="busy || !loaded" @update:model-value="save" />
    </div>
    <p v-if="error" role="alert" class="text-xs text-red-600 dark:text-red-400">
      {{ error }} <button v-if="!loaded" type="button" class="underline" :disabled="busy" @click="load">{{ t('common.retry') }}</button>
    </p>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { getIntelligenceVisibility, setIntelligenceVisibility } from '@/api/admin/intelligence'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const tr = (key: string) => t('channelMonitorV2.intelligence.' + key)
const enabled = ref(false)
const loaded = ref(false)
const busy = ref(false)
const error = ref('')

async function load() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    enabled.value = await getIntelligenceVisibility()
    loaded.value = true
  } catch (e) {
    loaded.value = false
    error.value = extractApiErrorMessage(e, t('tests.loadFailed'))
  } finally {
    busy.value = false
  }
}

async function save(value: boolean) {
  if (!loaded.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    enabled.value = await setIntelligenceVisibility(value)
  } catch (e) {
    error.value = extractApiErrorMessage(e, tr('saveError'))
  } finally {
    busy.value = false
  }
}
onMounted(load)
defineExpose({ load })
</script>
