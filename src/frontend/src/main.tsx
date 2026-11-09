import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { initPwaInstallPromptCapture, registerLattixServiceWorker } from './lib/pwa'
import { RequestStateProvider } from './lib/request-state'

// 安装提示早于设置页挂载，必须在应用启动时开始监听。
initPwaInstallPromptCapture()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <RequestStateProvider>
      <App />
    </RequestStateProvider>
  </StrictMode>,
)

// 开发服务器不注册 Service Worker，避免缓存 Vite 的未哈希模块。
if (import.meta.env.PROD) {
  registerLattixServiceWorker()
}
