package service

import (
    "context"
    "testing"
"math"
	"task137-gridflow/internal/domain"
	"task137-gridflow/internal/ybus"
)

func TestBug06_TransformerTapSurvivesAssemblyAndFlowModel(t *testing.T) {
	_ = context.Background()
	br := domain.Branch{ID:"T", FromBus:"A", ToBus:"B", X:0.1, Tap:1.2, InService:true}
	net := buildNetwork([]domain.Bus{{ID:"A", Type:domain.BusTypeSlack, VSpec:1}, {ID:"B", Type:domain.BusTypePQ}}, []domain.Branch{br}, nil, nil, nil, "A")
	if net.Branches[0].Tap != 1.2 { t.Errorf("assembled tap=%v, want 1.2", net.Branches[0].Tap) }
	ym, err := ybus.Build([]string{"A","B"}, []domain.Branch{br})
	if err != nil { t.Fatal(err) }
	if math.Abs(imag(ym.Get(0,0))+10/(1.2*1.2)) > 1e-9 { t.Errorf("from diagonal=%v, want tap-scaled admittance", ym.Get(0,0)) }
	from, to := ybus.BranchFlow(br, 1, 1)
	if ybus.Abs(from)+ybus.Abs(to) == 0 { t.Error("tap branch flow was treated as an untapped zero-flow line") }
}
