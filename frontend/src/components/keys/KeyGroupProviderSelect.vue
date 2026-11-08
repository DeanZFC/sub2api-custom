<template>
  <fieldset>
    <legend class="input-label">{{ t("keys.providerLabel") }}</legend>
    <div class="grid gap-2" :class="compact ? 'grid-cols-4' : 'grid-cols-2 sm:grid-cols-4'">
      <label
        v-for="provider in options"
        :key="provider.value"
        class="relative min-w-0"
        :class="
          provider.count === 0 ? 'cursor-not-allowed' : 'cursor-pointer'
        "
      >
        <input
          type="radio"
          :name="name"
          :value="provider.value"
          :checked="modelValue === provider.value"
          :disabled="provider.count === 0"
          class="peer sr-only"
          @change="$emit('update:modelValue', provider.value)"
        />
        <span
          class="flex h-full flex-col items-center gap-2 rounded-xl border border-gray-200 bg-white px-2 text-center transition-colors peer-checked:border-primary-500 peer-checked:bg-primary-50/60 peer-checked:ring-1 peer-checked:ring-primary-500 peer-focus-visible:outline peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-primary-500 peer-disabled:opacity-40 dark:border-dark-600 dark:bg-dark-800 dark:peer-checked:border-primary-500 dark:peer-checked:bg-primary-500/10"
          :class="
            [
              compact ? 'py-2' : 'py-3',
              provider.count > 0 && 'hover:border-primary-300 dark:hover:border-primary-700',
            ]
          "
        >
          <span
            class="flex items-center justify-center gap-1.5"
            :class="compact ? 'h-6' : 'h-8'"
            aria-hidden="true"
          >
            <span
              v-for="platform in KEY_GROUP_PROVIDER_ICONS[provider.value]"
              :key="platform"
              class="flex items-center justify-center rounded-lg"
              :class="[platformBadgeLightClass(platform), compact ? 'h-6 w-6' : 'h-8 w-8']"
            >
              <PlatformIcon :platform="platform" :size="compact ? 'md' : 'lg'" />
            </span>
          </span>
          <span
            class="font-semibold text-gray-800 dark:text-gray-100"
            :class="compact ? 'text-xs' : 'text-sm'"
            >{{ provider.label }}</span
          >
        </span>
        <span
          v-if="modelValue === provider.value"
          class="absolute right-1.5 top-1.5 flex h-4 w-4 items-center justify-center rounded-full bg-primary-500 text-white"
          aria-hidden="true"
        >
          <Icon name="check" size="xs" :stroke-width="3" />
        </span>
      </label>
    </div>
    <p
      class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400"
      aria-live="polite"
    >
      {{
        options.every((provider) => provider.count === 0)
          ? t("common.noGroupsAvailable")
          : t(`keys.providerHints.${modelValue}`)
      }}
    </p>
  </fieldset>
</template>

<script setup lang="ts">
import { useI18n } from "vue-i18n";
import Icon from "@/components/icons/Icon.vue";
import PlatformIcon from "@/components/common/PlatformIcon.vue";
import { platformBadgeLightClass } from "@/utils/platformColors";
import { KEY_GROUP_PROVIDER_ICONS, type KeyGroupProvider } from "@/utils/keyGroupProviders";

withDefaults(defineProps<{
  modelValue: KeyGroupProvider;
  options: { value: KeyGroupProvider; label: string; count: number }[];
  name?: string;
  compact?: boolean;
}>(), { name: "key-provider", compact: false });

defineEmits<{ "update:modelValue": [value: KeyGroupProvider] }>();
const { t } = useI18n();
</script>
