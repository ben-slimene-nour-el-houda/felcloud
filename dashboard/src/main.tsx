import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { createApi } from './api';
import { AppProviders, AppRoutes } from './App';
import { loadConfig } from './config';
import './styles/index.css';

async function start() {
  const config = loadConfig();
  const api = await createApi(config);
  createRoot(document.getElementById('root') as HTMLElement).render(
    <StrictMode>
      <AppProviders api={api} config={config} pollMs={api.mode === 'demo' ? 700 : 2000}>
        <BrowserRouter>
          <AppRoutes />
        </BrowserRouter>
      </AppProviders>
    </StrictMode>,
  );
}

void start();
