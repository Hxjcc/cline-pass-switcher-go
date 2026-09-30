package store

// Health describes known persistence failures without probing the disk on
// every request. Details are intended for the authenticated console only.
type Health struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func (s *Store) Health() Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthLocked()
}

func (s *Store) healthLocked() Health {
	if s.closed {
		return Health{Status: "unavailable", Message: "数据存储已关闭"}
	}
	if s.writeErr != nil || s.recordErr != nil {
		err := s.writeErr
		if err == nil {
			err = s.recordErr
		}
		return Health{
			Status:  "unavailable",
			Message: "无法可靠保存请求费用，已暂停有额度上限的密钥的新生成和压缩请求。请修复存储故障、核对失败期间的费用后重启服务。",
			Detail:  err.Error(),
		}
	}
	if s.checkpointErr != nil {
		return Health{
			Status:  "degraded",
			Message: "数据快照合并失败，已提交的数据仍保存在日志中，请求和记账可继续。请检查数据目录的空间和权限。",
			Detail:  s.checkpointErr.Error(),
		}
	}
	return Health{Status: "ok"}
}
