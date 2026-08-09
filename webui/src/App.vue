<template>
  <el-container v-if="route.path !== '/login'" class="layout">
    <el-aside width="200px" class="aside">
      <div class="logo">
        <el-icon :size="22"><Connection /></el-icon>
        <span>NodeX 节点管家</span>
      </div>
      <el-menu :default-active="route.path" router background-color="#1d2129" text-color="#a3a6ad" active-text-color="#409eff">
        <el-menu-item index="/"><el-icon><Odometer /></el-icon>仪表盘</el-menu-item>
        <el-menu-item index="/panel"><el-icon><Link /></el-icon>面板对接</el-menu-item>
        <el-menu-item index="/nodes"><el-icon><Collection /></el-icon>节点管理</el-menu-item>
        <el-menu-item index="/logs"><el-icon><Document /></el-icon>运行日志</el-menu-item>
        <el-menu-item index="/system"><el-icon><Tools /></el-icon>系统设置</el-menu-item>
      </el-menu>
    </el-aside>
    <el-main class="main">
      <router-view />
    </el-main>
  </el-container>
  <router-view v-else />
</template>

<script setup>
import { useRoute } from 'vue-router'
import { Connection, Odometer, Collection, Document, Tools, Link } from '@element-plus/icons-vue'
const route = useRoute()
</script>

<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: #f5f7fa; font-family: 'PingFang SC', 'Microsoft YaHei', sans-serif; }
.layout { height: 100vh; }
.aside { background: #1d2129; }
.aside .logo { display: flex; align-items: center; gap: 8px; color: #fff; font-weight: 600; padding: 18px 16px; font-size: 15px; }
.aside .el-menu { border-right: none; }
.main { padding: 20px; overflow-y: auto; }
.card { background: #fff; border-radius: 8px; padding: 20px; margin-bottom: 16px; box-shadow: 0 1px 4px rgba(0,0,0,.06); }
.card-title { font-size: 15px; font-weight: 600; margin-bottom: 16px; display: flex; align-items: center; gap: 8px; }
</style>
