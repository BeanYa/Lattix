import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { RequestStateProvider } from './lib/request-state.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <RequestStateProvider>
      <App />
    </RequestStateProvider>
  </StrictMode>,
)
