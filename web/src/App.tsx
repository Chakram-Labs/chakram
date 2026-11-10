import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { ThemeProvider } from './context/ThemeContext';
import Layout from './components/Layout';
import Overview from './pages/Overview';
import Validator from './pages/Validator';
import Blocks from './pages/Blocks';
import BlockDetail from './pages/Blocks';
import Tx from './pages/Tx';
import Wallet from './pages/Wallet';
import Peers from './pages/Peers';
import Logs from './pages/Logs';
import Settings from './pages/Settings';
import Staking from './pages/Staking';

export default function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<Overview />} />
            <Route path="validator" element={<Validator />} />
            <Route path="blocks" element={<Blocks />} />
            <Route path="block/:height" element={<BlockDetail />} />
            <Route path="tx/:hash" element={<Tx />} />
            <Route path="staking" element={<Staking />} />
            <Route path="wallet" element={<Wallet />} />
            <Route path="peers" element={<Peers />} />
            <Route path="logs" element={<Logs />} />
            <Route path="settings" element={<Settings />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </ThemeProvider>
  );
}
