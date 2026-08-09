<template>
  <div>
    <div class="card">
      <div class="card-title">
        <el-icon><Collection /></el-icon>节点管理
        <el-button type="primary" size="small" style="margin-left:auto" :icon="Plus" @click="addNode">新增节点</el-button>
      </div>
      <el-table :data="nodes" v-loading="loading" empty-text="暂无节点，点击右上角新增">
        <el-table-column label="节点" min-width="200">
          <template #default="{ row }">
            <div class="node-name">
              <el-tag type="primary" size="small" effect="plain" style="margin-right:6px">{{ protoLabel(row.protocol) }}</el-tag>
              <el-tag :type="row.enabled ? (row.xray.running ? 'success' : 'warning') : 'info'" size="small" style="margin-right:6px">
                {{ row.enabled ? (row.xray.running ? '运行中' : '已停止') : '已禁用' }}
              </el-tag>
              <b>{{ row.name }}</b>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="Xray" width="150">
          <template #default="{ row }">
            <div class="ver">{{ row.xray.version }}</div>
            <div class="ver-sub">{{ row.xray.running ? 'PID ' + row.xray.pid : '—' }}</div>
          </template>
        </el-table-column>
        <el-table-column label="Hysteria2" width="150">
          <template #default="{ row }">
            <div class="ver">{{ row.hy2.version }}</div>
            <div class="ver-sub">{{ row.hy2.running ? 'PID ' + row.hy2.pid : '—' }}</div>
          </template>
        </el-table-column>
        <el-table-column label="面板同步" min-width="200">
          <template #default="{ row }">
            <div v-if="row.panel.running" class="sync-ok">
              <el-icon><CircleCheck /></el-icon>
              <span>{{ row.panel.lastError ? '有错误' : '正常' }}</span>
            </div>
            <div v-else class="sync-off">未运行</div>
            <div class="sync-time">{{ row.panel.lastSync }}</div>
            <div v-if="row.panel.lastError" class="sync-err" :title="row.panel.lastError">{{ row.panel.lastError }}</div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="260" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="success" :disabled="!row.enabled" @click="act('restart', row.id)">重启</el-button>
            <el-button size="small" type="warning" :disabled="!row.enabled" @click="act('stop', row.id)">停止</el-button>
            <el-button size="small" :icon="Edit" @click="$router.push('/nodes/' + row.id)" />
            <el-button size="small" type="danger" :icon="Delete" @click="removeNode(row)" />
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Collection, Plus, Edit, Delete, CircleCheck } from '@element-plus/icons-vue'
import { api } from '../api'

const PROTO_LABELS = { vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', shadowsocks: 'SS', hysteria2: 'Hysteria2' }
const protoLabel = (p) => PROTO_LABELS[p] || p || '未知'
const router = useRouter()
const nodes = ref([])
const loading = ref(false)

async function refresh() {
  loading.value = true
  try {
    const res = await api.get('/api/status')
    nodes.value = res.nodes
  } catch (e) {} finally { loading.value = false }
}

async function act(action, id) {
  try {
    await api.post('/api/action', { action, node_id: id })
    ElMessage.success('操作成功')
    setTimeout(refresh, 2000)
  } catch (e) { ElMessage.error(e.message) }
}

async function addNode() {
  try {
    const cfg = await api.get('/api/config')
    const node = {
      id: 'n' + Math.random().toString(16).slice(2, 6),
      name: '新节点' + (cfg.nodes.length + 1),
      enabled: true,
      node_id: 1,
      node_type: '',
      node: {
        protocol: 'vless', port: 8686, uuid: '', tls: 0, cert_path: '', key_path: '', server_name: '',
        reality: { dest: 'www.amazon.com:443', server_names: 'www.amazon.com', private_key: '', public_key: '', short_ids: '' },
        hy2: { port: 9443, password: '', obfs: 'none', obfs_password: '', up_mbps: 100, down_mbps: 1000, ignore_bw: false, cert_path: '', key_path: '' },
        ss_method: '2022-blake3-aes-128-gcm'
      }
    }
    cfg.nodes.push(node)
    await api.put('/api/config', cfg)
    ElMessage.success('节点已创建，正在打开编辑页...')
    setTimeout(() => router.push('/nodes/' + node.id), 500)
  } catch (e) { ElMessage.error(e.message) }
}

async function removeNode(row) {
  try {
    await ElMessageBox.confirm(`确定删除节点「${row.name}」？`, '删除节点', { type: 'warning' })
    const cfg = await api.get('/api/config')
    cfg.nodes = cfg.nodes.filter(n => n.id !== row.id)
    await api.put('/api/config', cfg)
    ElMessage.success('已删除')
    refresh()
  } catch (e) {
    if (e !== 'cancel') ElMessage.error(e.message || e)
  }
}

onMounted(refresh)
</script>

<style scoped>
.node-name { display: flex; align-items: center; gap: 6px; }
.node-id { color: #c0c4cc; font-size: 12px; }
.ver { font-size: 12px; color: #606266; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 140px; }
.ver-sub { font-size: 12px; color: #c0c4cc; }
.sync-ok { display: flex; align-items: center; gap: 4px; color: #67c23a; font-size: 13px; }
.sync-off { color: #c0c4cc; font-size: 13px; }
.sync-time { font-size: 12px; color: #c0c4cc; }
.sync-err { font-size: 12px; color: #f56c6c; max-width: 190px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
