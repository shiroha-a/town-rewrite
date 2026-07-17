<script setup lang="ts">
/**
 * Command-bar icon. White line motifs based on Feather Icons (MIT-licensed),
 * drawn with currentColor on top of the raised green buttons defined in
 * style.css. The `name` matches the legacy command image key (reload/go_work/…).
 */
defineProps<{ name: string }>();
</script>

<template>
  <svg
    class="cmd-icon"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <!-- 更新(回転矢印) -->
    <g v-if="name === 'reload'">
      <path d="M23 4v6h-6" />
      <path d="M1 20v-6h6" />
      <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10" />
      <path d="M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
    </g>
    <!-- 仕事(ブリーフケース) -->
    <g v-else-if="name === 'go_work'">
      <rect x="2" y="7" width="20" height="14" rx="2" />
      <path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16" />
    </g>
    <!-- アイテム使用(ショッピングバッグ) -->
    <g v-else-if="name === 'item'">
      <path d="M6 2 3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4Z" />
      <path d="M3 6h18" />
      <path d="M16 10a4 4 0 0 1-8 0" />
    </g>
    <!-- メール(封筒) -->
    <g v-else-if="name === 'mail'">
      <rect x="2" y="4" width="20" height="16" rx="2" />
      <path d="m22 7-10 5L2 7" />
    </g>
    <!-- キャラ作成(人物) -->
    <g v-else-if="name === 'doukyo'">
      <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
      <circle cx="12" cy="7" r="4" />
    </g>
    <!-- あいさつ(吹き出し) -->
    <g v-else-if="name === 'aisatu'">
      <path
        d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5Z"
      />
    </g>
    <!-- ログアウト(電源) -->
    <g v-else-if="name === 'off'">
      <path d="M18.36 6.64a9 9 0 1 1-12.73 0" />
      <line x1="12" y1="2" x2="12" y2="12" />
    </g>
    <!-- イベント通知(ベル) -->
    <g v-else-if="name === 'event'">
      <path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9" />
      <path d="M13.73 21a2 2 0 0 1-3.46 0" />
    </g>
  </svg>
</template>
