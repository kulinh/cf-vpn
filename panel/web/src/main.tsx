import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { App } from './app/App'
import { LoginGate } from './app/LoginGate'
import './styles/tailwind.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <LoginGate>
        <App />
      </LoginGate>
    </BrowserRouter>
  </StrictMode>,
)
