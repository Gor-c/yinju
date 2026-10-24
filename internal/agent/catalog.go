package agent

// Catalog 是「本次运行有哪些工具可用」的声明式表达，与 Registry 分工不同：
//
//	Registry  面向模型：只关心 Tool 能否被执行
//	Catalog   面向装配层：知道每个工具的名字、归属、用途，能按要求裁剪成 Registry
//
// 分出这一层是为了让「新增一个工具」变成往目录里加一条记录，
// 而不是在三个地方分别改白名单、顺序表和注册逻辑。
type Catalog struct {
	entries []catalogEntry
}

type catalogEntry struct {
	spec   ToolSpec
	tool   Tool
	always bool
}

// ToolSpec 是工具的静态元数据。校验上传定义、设置页展示、文档生成
// 都基于它，不需要构造可执行实例。
type ToolSpec struct {
	Name        string
	Namespace   string
	Description string
}

func NewCatalog() *Catalog { return &Catalog{} }

// Add 注册一个受白名单约束的工具：技能/Agent 声明了才会启用。
func (c *Catalog) Add(spec ToolSpec, tool Tool) {
	c.add(spec, tool, false)
}

// AddAlways 注册一个不受白名单约束的工具，用于「用户本轮显式开启的运行时能力」
// （例如联网搜索）。它不应该被技能的 tools 列表误砍掉。
func (c *Catalog) AddAlways(spec ToolSpec, tool Tool) {
	c.add(spec, tool, true)
}

func (c *Catalog) add(spec ToolSpec, tool Tool, always bool) {
	if tool == nil || spec.Name == "" {
		return
	}
	c.entries = append(c.entries, catalogEntry{spec: spec, tool: tool, always: always})
}

func (c *Catalog) Specs() []ToolSpec {
	out := make([]ToolSpec, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry.spec)
	}
	return out
}

func (c *Catalog) Names() []string {
	out := make([]string, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry.spec.Name)
	}
	return out
}

func (c *Catalog) Has(name string) bool {
	for _, entry := range c.entries {
		if entry.spec.Name == name {
			return true
		}
	}
	return false
}

// Registry 按名字裁剪出可执行集合。names 为空表示「全部受控工具」，
// always 类型的工具始终保留。
func (c *Catalog) Registry(names []string) *Registry {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	registry := NewRegistry()
	for _, entry := range c.entries {
		if !entry.always && len(wanted) > 0 && !wanted[entry.spec.Name] {
			continue
		}
		registry.Add(entry.tool)
	}
	return registry
}

// Select 从一组候选名字里挑出目录中真实存在的，并保持目录声明顺序。
// 用于把用户声明（上传的定义文件、项目配置）规范化成稳定顺序。
func (c *Catalog) Select(names []string) []string {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	out := make([]string, 0, len(wanted))
	for _, entry := range c.entries {
		if entry.always {
			continue
		}
		if wanted[entry.spec.Name] {
			out = append(out, entry.spec.Name)
		}
	}
	return out
}
