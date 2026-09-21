<template>
  <section class="min-w-0 space-y-3 rounded-lg border p-3" :class="outcome === 'pass' ? 'border-green-200 bg-green-50/40 dark:border-green-900 dark:bg-green-950/10' : 'border-amber-200 bg-amber-50/40 dark:border-amber-900 dark:bg-amber-950/10'" :data-outcome="outcome">
    <h5 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t(`admin.tests.protection.actions.${outcome}`) }}</h5>
    <label class="input-label block">
      {{ t('admin.tests.protection.actions.scheduling') }}
      <select :value="action.scheduling" class="input mt-1 w-full" data-action-scheduling @change="patch({ scheduling: ($event.target as HTMLSelectElement).value as TestOutcomeAction['scheduling'] })">
        <option value="keep">{{ t('admin.tests.protection.actions.keepScheduling') }}</option>
        <option value="pause">{{ t('admin.tests.protection.actions.pause') }}</option>
        <option value="resume">{{ t('admin.tests.protection.actions.resume') }}</option>
      </select>
    </label>
    <label class="input-label block">
      {{ t('admin.tests.protection.actions.groups') }}
      <select :value="action.group_mode" class="input mt-1 w-full" data-action-group-mode @change="setGroupMode(($event.target as HTMLSelectElement).value as TestOutcomeAction['group_mode'])">
        <option value="keep">{{ t('admin.tests.protection.actions.keepGroups') }}</option>
        <option value="assign">{{ t('admin.tests.protection.actions.assignGroups') }}</option>
      </select>
    </label>
    <template v-if="action.group_mode === 'assign'">
      <div class="space-y-2">
        <p class="text-xs font-medium text-gray-700 dark:text-gray-300">{{ t('admin.tests.protection.actions.selectedGroups', { count: selectedIDs.length }) }}</p>
        <div v-if="selectedIDs.length" class="flex flex-wrap gap-1.5" data-selected-groups>
          <button v-for="id in selectedIDs" :key="id" type="button" class="inline-flex max-w-full items-center gap-1 rounded border border-gray-200 bg-white px-2 py-1 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200" :aria-label="t('admin.tests.protection.actions.removeGroup', { group: groupLabel(id) })" @click="setGroup(id, false)">
            <span class="truncate">{{ groupLabel(id) }}</span><span aria-hidden="true">×</span>
          </button>
        </div>
        <input v-model="search" type="search" class="input w-full text-sm" :placeholder="t('admin.tests.protection.actions.searchGroups')" :aria-label="t('admin.tests.protection.actions.searchGroups')" data-group-search />
        <div class="max-h-40 space-y-1 overflow-y-auto rounded-md border border-gray-200 bg-white p-2 dark:border-dark-600 dark:bg-dark-900">
          <label v-for="group in filteredGroups" :key="group.id" class="flex cursor-pointer items-start gap-2 rounded px-2 py-1.5 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-800">
            <input type="checkbox" :checked="selectedIDs.includes(group.id)" :disabled="selectedIDs.length >= 100 && !selectedIDs.includes(group.id)" class="mt-0.5" :data-action-group="group.id" @change="setGroup(group.id, ($event.target as HTMLInputElement).checked)" />
            <span class="min-w-0 break-words">{{ group.name }} <span class="text-xs text-gray-400">#{{ group.id }}</span></span>
          </label>
          <p v-if="!filteredGroups.length" class="px-2 py-2 text-xs text-gray-500">{{ t('common.noGroupsAvailable') }}</p>
        </div>
        <p v-if="unavailableIDs.length" class="text-xs text-red-600 dark:text-red-400" role="alert" data-unavailable-groups>{{ t('admin.tests.protection.actions.unavailableGroups', { ids: unavailableIDs.join(', ') }) }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.actions.emptyGroupsHint') }}</p>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminGroup, TestOutcomeAction } from '@/types'
import { defaultTestOutcomeAction } from '@/utils/testProtection'

const props = defineProps<{ modelValue?: TestOutcomeAction; outcome: 'pass' | 'fail'; groups: AdminGroup[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: TestOutcomeAction] }>()
const { t } = useI18n()
const search = ref('')
const action = computed(() => props.modelValue || defaultTestOutcomeAction(props.outcome))
const selectedIDs = computed(() => action.value.group_ids || [])
const unavailableIDs = computed(() => selectedIDs.value.filter(id => !props.groups.some(group => group.id === id)))
const filteredGroups = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return props.groups.filter(group => !query || `${group.name} ${group.id}`.toLocaleLowerCase().includes(query))
})
const groupLabel = (id: number) => {
  const group = props.groups.find(item => item.id === id)
  return group ? `${group.name} (#${id})` : `#${id}`
}
const patch = (value: Partial<TestOutcomeAction>) => emit('update:modelValue', { ...action.value, ...value })
const setGroupMode = (group_mode: TestOutcomeAction['group_mode']) => patch({ group_mode, group_ids: group_mode === 'keep' ? undefined : [...selectedIDs.value] })
const setGroup = (id: number, selected: boolean) => patch({ group_ids: selected ? [...new Set([...selectedIDs.value, id])] : selectedIDs.value.filter(value => value !== id) })
</script>
