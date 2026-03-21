import { useState, useEffect } from 'react'
import './App.css'
function App() {
  const [data, setData] = useState(null)
  const [artifacts, setArtifacts] = useState(null)
  const [error, setError] = useState(null)
  const [hoverNode, setHoverNode] = useState(null)

  // 1. 每秒轮询后端接口
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

      fetch('http://localhost:8081/api/artifacts')
        .then(res => res.ok ? res.json() : null)
        .then(json => { if (json) setArtifacts(json) })
        .catch(() => {})
    }

    fetchData()
    const intervalId = setInterval(fetchData, 1000)
    return () => clearInterval(intervalId)
  }, [])

  // 2. 根据节点状态染色
  const getStatusColor = (node) => {
    if (node.Status === 3) return '#ff4d4f'
    if (node.Status === 2) return '#52c41a'
    if (node.Status === 1) return '#1890ff'
    if (node.WetherTraveled) return '#faad14'
    return '#d9d9d9'
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
              {node.Result && (
                <div className="node-result">
                  {node.Result.length > 50 ? node.Result.substring(0, 50) + '...' : node.Result}
                </div>
              )}
              {node.key_facts && node.key_facts.length > 0 && (
                <div className="node-facts-badge">{node.key_facts.length} facts</div>
              )}
              {node.artifact_refs && node.artifact_refs.length > 0 && (
                <div className="node-artifact-badge">{node.artifact_refs.length} artifacts</div>
              )}
            </div>
          </div>
        </div>

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

        {hoverNode.Result && (
          <div className="tooltip-section">
            <strong>Result:</strong>
            <pre>{hoverNode.Result}</pre>
          </div>
        )}

        {hoverNode.key_facts && hoverNode.key_facts.length > 0 && (
          <div className="tooltip-section">
            <strong>Key Facts:</strong>
            <ul>{hoverNode.key_facts.map((f, i) => <li key={i}>{f}</li>)}</ul>
          </div>
        )}

        {hoverNode.artifact_refs && hoverNode.artifact_refs.length > 0 && (
          <div className="tooltip-section">
            <strong>Artifact Refs:</strong>
            <span>{hoverNode.artifact_refs.join(', ')}</span>
          </div>
        )}

        {hoverNode.handoff && (
          <div className="tooltip-section">
            <strong>Handoff:</strong>
            <pre>{hoverNode.handoff}</pre>
          </div>
        )}

        {hoverNode.Variables && Object.keys(hoverNode.Variables).length > 0 && (
          <div className="tooltip-section">
            <strong>Variables (Scoped):</strong>
            <pre>{JSON.stringify(hoverNode.Variables, null, 2)}</pre>
          </div>
        )}
      </div>
    )
  }

  const renderArtifactPanel = () => {
    if (!artifacts || !artifacts.artifacts || artifacts.artifacts.length === 0) return null;
    return (
      <aside className="artifact-panel">
        <h2>Artifacts ({artifacts.counter})</h2>
        <div className="artifact-list">
          {artifacts.artifacts.map(art => (
            <div key={art.id} className={`artifact-item ${art.evicted ? 'evicted' : ''} ${art.pinned ? 'pinned' : ''}`}>
              <span className="artifact-id">{art.id}</span>
              <span className="artifact-type">{art.type}</span>
              <span className="artifact-summary">{art.summary}</span>
              {art.pinned && <span className="artifact-badge">PIN</span>}
              {art.evicted && <span className="artifact-badge evicted-badge">EVICTED</span>}
            </div>
          ))}
        </div>
      </aside>
    )
  }

  return (
    <div className="app-container">
      <header>
        <h1>LLMVM Visualizer</h1>
        {error && <span className="error">Error: {error}</span>}
        {artifacts && <span className="artifact-count">Artifacts: {artifacts.counter}</span>}
      </header>

      <div className="main-layout">
        <main className="tree-canvas">
          {data ? renderNode(data) : <p>Loading state from engine...</p>}
        </main>

        {renderArtifactPanel()}
      </div>

      {renderTooltip()}
    </div>
  )
}

export default App
