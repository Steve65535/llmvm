import { useState, useEffect } from 'react'
import './App.css'
function App() {
  const [data, setData] = useState(null)
  const [error, setError] = useState(null)
  const [hoverNode, setHoverNode] = useState(null)

  // 1. 每秒轮询一次后端接口
  useEffect(() => {
    const fetchData = () => {
      fetch('http://localhost:8081/api/state')
        .then(res => {
          if (!res.ok) throw new Error(res.statusText)
          return res.json()
        })
        .then(json => {
          setData(json)
          setError(null)
        })
        .catch(err => {
          setError(err.message)
        })
    }

    fetchData()
    const intervalId = setInterval(fetchData, 1000)
    return () => clearInterval(intervalId)
  }, [])

  // 2. 根据节点状态染色
  // Status: 0=Pending, 1=Running, 2=Completed, 3=Failed
  const getStatusColor = (node) => {
    if (node.Status === 3) return '#ff4d4f' // Failed: Red
    if (node.Status === 2) return '#52c41a' // Completed: Green
    if (node.Status === 1) return '#1890ff' // Running: Blue
    if (node.WetherTraveled) return '#faad14' // Traveled but pending/other: Orange
    return '#d9d9d9' // Default Pending: Gray
  }

  // 3. 递归渲染树节点
  const renderNode = (node) => {
    if (!node) return null;

    const isCursor = data && data.ID === node.ID;

    return (
      <div key={node.ID} className="node-container">
        <div className="node-wrapper">
          <div
            className={`node-box ${isCursor ? 'cursor-pulse' : ''}`}
            style={{
              borderColor: getStatusColor(node),
              boxShadow: isCursor ? `0 0 10px ${getStatusColor(node)}` : 'none'
            }}
            onMouseEnter={() => setHoverNode(node)}
            onMouseLeave={() => setHoverNode(null)}
          >
            <div className="node-header" style={{ backgroundColor: getStatusColor(node) }} />
            <div className="node-body">
              <strong>{node.Name}</strong>
              <div className="node-type">Type: {['Normal', 'Loop', 'Leaf'][node.Type]}</div>
              {/* 特别是在报错的时候展示 Result */}
              {node.Result && (
                <div className="node-result">
                  {node.Result.length > 50 ? node.Result.substring(0, 50) + '...' : node.Result}
                </div>
              )}
            </div>
          </div>
        </div>

        {/* 子节点 */}
        {node.Children && node.Children.length > 0 && (
          <div className="children-container">
            {node.Children.map(child => renderNode(child))}
          </div>
        )}
      </div>
    )
  }

  const renderTooltip = () => {
    if (!hoverNode) return null;
    return (
      <div className="tooltip">
        <h3>{hoverNode.Name} ({hoverNode.ID})</h3>
        <p><strong>Status:</strong> {['Pending', 'Running', 'Completed', 'Failed'][hoverNode.Status]}</p>
        <p><strong>Traveled:</strong> {hoverNode.WetherTraveled ? 'Yes' : 'No'}</p>
        <p><strong>Finished:</strong> {hoverNode.WetherFinished ? 'Yes' : 'No'}</p>

        {hoverNode.Information && hoverNode.Information.length > 0 && (
          <div className="tooltip-section">
            <strong>Information:</strong>
            <pre>{hoverNode.Information.join('\n')}</pre>
          </div>
        )}

        {hoverNode.Variables && Object.keys(hoverNode.Variables).length > 0 && (
          <div className="tooltip-section">
            <strong>Variables (Scoped):</strong>
            <pre>{JSON.stringify(hoverNode.Variables, null, 2)}</pre>
          </div>
        )}

        {hoverNode.Result && (
          <div className="tooltip-section">
            <strong>Result/Error:</strong>
            <pre>{hoverNode.Result}</pre>
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="app-container">
      <header>
        <h1>LLMVM Visualizer</h1>
        {error && <span className="error">Error: {error}</span>}
      </header>

      <main className="tree-canvas">
        {data ? renderNode(data) : <p>Loading state from engine...</p>}
      </main>

      {renderTooltip()}
    </div>
  )
}

export default App
