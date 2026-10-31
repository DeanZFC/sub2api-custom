<template>
  <div class="mt-4 border-t border-gray-200 pt-4 dark:border-dark-400">
    <label class="input-label">{{ t('admin.groups.unavailableFallback.title') }}</label>
    <Select
      :model-value="modelValue"
      :options="options"
      :disabled="loading"
      @update:model-value="emit('update:modelValue', $event as number | null)"
    />
    <p class="input-hint">{{ t('admin.groups.unavailableFallback.hint') }}</p>
    <button v-if="failed" type="button" class="mt-2 text-sm text-red-500" @click="load">
      {{ t('admin.groups.unavailableFallback.loadFailed') }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { AdminGroup } from '@/types'

const props = defineProps<{ modelValue: number | null; platform: string; groupId?: number }>()
const emit = defineEmits<{ 'update:modelValue': [value: number | null] }>()
const { t } = useI18n()
const groups = ref<AdminGroup[]>([])
const loading = ref(false)
const failed = ref(false)
const options = computed(() => {
  const result: { value: number | null; label: string }[] = [
    { value: null, label: t('admin.groups.unavailableFallback.none') }
  ]
  for (const group of groups.value) {
    if (group.id !== props.groupId && group.platform === props.platform && group.status === 'active') {
      result.push({ value: group.id, label: group.name })
    }
  }
  // Keep a disabled/deleted target visible until the administrator clears it.
  if (props.modelValue != null && !result.some(option => option.value === props.modelValue)) {
    result.push({ value: props.modelValue, label: `#${props.modelValue}` })
  }
  return result
})
async function load() {
  loading.value = true
  failed.value = false
  try {
    groups.value = (await adminAPI.groups.getAll()) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>
