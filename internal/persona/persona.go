// 人设定义
package persona

// Persona Bot 人设，控制系统提示词和可用工具
type Persona struct {
	Name         string
	SystemPrompt string
	Tools        []string
	Model        string
}
