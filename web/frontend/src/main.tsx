import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { registerChatExtensions } from './lib/chat-extensions'
import './index.css'

registerChatExtensions()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
