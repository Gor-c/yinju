<template>
  <div class="nf-page">
    <div class="nf-code">404</div>
    <h1>这个页面不存在</h1>
    <p>链接可能已过期，或者地址输入有误。可以从下面两个入口继续。</p>
    <div class="nf-actions">
      <el-button type="primary" @click="$router.push('/agent')">回到对话</el-button>
      <el-button @click="$router.push('/units')">查看知识库</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
</script>

<style scoped>
.nf-page {
  max-width: 520px;
  margin: 0 auto;
  padding: calc(80px * var(--fs-scale)) 24px;
  text-align: center;
}

.nf-code {
  font-size: calc(40px * var(--fs-scale));
  font-weight: 500;
  letter-spacing: 0.08em;
  color: var(--text-muted);
  line-height: 1.2;
}

.nf-page h1 {
  margin: calc(12px * var(--fs-scale)) 0 calc(8px * var(--fs-scale));
  font-size: calc(20px * var(--fs-scale));
  font-weight: 500;
  color: var(--text);
}

.nf-page p {
  margin: 0 0 calc(24px * var(--fs-scale));
  font-size: calc(13px * var(--fs-scale));
  color: var(--text-secondary);
  line-height: 1.7;
}

.nf-actions {
  display: flex;
  justify-content: center;
  gap: 12px;
}
</style>
