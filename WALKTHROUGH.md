# Evo-Sched Walkthrough

Bu dosyanın amacı projede nereye gitmek istediğimizi, şu ana kadar ne yaptığımızı ve hangi aşamada olduğumuzu geriye dönük olarak takip edilebilir hale getirmektir.

## 1. Amaç ve Hedeflenen Yol

Projenin ana hedefi, Kubernetes içinde çalışan kendi scheduler'ımızı geliştirmek ve daha sonra bu scheduler'ın karar verme politikasını evolutionary optimization ile iyileştirmektir.

Proje iki ana katmandan oluşur:

1. Gerçek Kubernetes üzerinde çalışan custom scheduler
2. Bu scheduler'ın scoring/decision parametrelerini optimize eden evolutionary algorithm

İlk aşamadaki teknik hedef:

- `schedulerName: evok8s-scheduler` verilen pod'ları default scheduler yerine bizim scheduler'ın ele alması
- Uygun node'ları görmesi
- Basit bir strateji ile node seçmesi
- Pod'u seçilen node'a bind etmesi

Bu ilk versiyonda amaç "önce çalışan sistem" yaklaşımıdır. Zeka ve optimizasyon daha sonra eklenecektir.

Planlanan yol kabaca şöyledir:

1. Local Kubernetes ortamını kurmak
2. Basit bir custom scheduler yazmak
3. Scheduler'ı cluster içine deployment olarak koymak
4. Gerçek pod'lar üzerinde scheduler davranışını test etmek
5. Karar mekanizmasını parametreli hale getirmek
6. Tekrar üretilebilir workload/trace tabanı kurmak
7. Fitness tanımlamak
8. Evolutionary algorithm ile policy parametrelerini optimize etmek
9. Baseline scheduler'larla karşılaştırmak
10. Sonuçları raporlamak

## 2. Bu Session'dan Önceki Durum

Bu session başlamadan önce repo ve cluster tarafında şu durum vardı:

### Hazır olanlar

- `kind` tabanlı local Kubernetes cluster kurulmuştu
- Cluster topolojisi [kind-config.yaml](./kind-config.yaml) içinde tanımlıydı
- Cluster çalışıyordu ve context `kind-evo-sched` idi
- `evo-sched` namespace mevcuttu
- Scheduler kodu [scheduler/main.go](./scheduler/main.go) içinde yazılmıştı
- Scheduler, `schedulerName == evok8s-scheduler` olan ve henüz schedule edilmemiş pod'ları tarayacak şekilde çalışıyordu
- Node seçimi için basit `least-pod-count` benzeri bir strateji vardı
- Pod binding mantığı kod içinde mevcuttu
- Docker image build edilmişti: `evok8s-scheduler:0.1.0`
- RBAC manifesti [rbac.yaml](./rbac.yaml) içinde mevcuttu
- ServiceAccount / ClusterRole / ClusterRoleBinding cluster'a uygulanmış durumdaydı

### Eksik olanlar

- Scheduler'ı cluster içinde çalıştıran bir deployment manifesti yoktu
- Repo kökünde `deploy.yaml` bulunmuyordu
- `evo-sched` namespace içinde çalışan custom scheduler pod'u yoktu
- `schedulerName: evok8s-scheduler` kullanan test workload manifesti yoktu
- Bu nedenle custom scheduler'ın gerçekten bind yaptığı henüz uçtan uca doğrulanmamıştı

Özetle, custom scheduler kodu ve gerekli yetkiler hazırlanmıştı; fakat scheduler henüz cluster içine deploy edilmemişti ve gerçek workload üzerinde test edilmemişti.

## 3. Bu Session'da Yaptıklarımız

Bu session'da amaç, 1. katmanda "çalışan gerçek scheduler" noktasına ulaşmaktı.

### 3.1. Mevcut durumu doğruladık

Önce repo ve cluster'ı kontrol ettik:

- Cluster'ın çalıştığını doğruladık
- Node'ların `Ready` durumda olduğunu gördük
- `evo-sched` namespace'in var olduğunu doğruladık
- RBAC kaynaklarının cluster'da bulunduğunu gördük
- Buna karşılık `evo-sched` namespace içinde çalışan scheduler pod'u olmadığını tespit ettik

Bu kontrol bize projenin tam olarak şu noktada olduğunu gösterdi:

- Cluster hazır
- Scheduler kodu hazır
- RBAC hazır
- Deployment ve gerçek test eksik

### 3.2. Scheduler deployment manifestini oluşturduk

Repo köküne [deploy.yaml](./deploy.yaml) dosyası eklendi.

Bu manifest ile:

- `evok8s-scheduler` isimli bir `Deployment` tanımlandı
- Namespace olarak `evo-sched` kullanıldı
- `serviceAccountName: evok8s-scheduler` ayarlandı
- Image olarak `evok8s-scheduler:0.1.0` kullanıldı
- `SCHEDULER_NAME=evok8s-scheduler` env variable'ı verildi

Bu adım, custom scheduler binary'sini cluster içinde çalışan gerçek bir pod haline getirdi.

### 3.3. Image'ı kind cluster node'larına yükledik

Local Docker image'ın kind node'ları tarafından kullanılabilmesi için image cluster'a yüklendi.

Uygulanan adım:

- `evok8s-scheduler:0.1.0` image'ı `evo-sched-control-plane`, `evo-sched-worker` ve `evo-sched-worker2` node'larına taşındı

Bu adım gerekliydi; aksi halde deployment ayağa kalksa bile image çekme hatası alınabilirdi.

### 3.4. Manifestleri cluster'a uyguladık

Şu kaynaklar apply edildi:

- [rbac.yaml](./rbac.yaml)
- [deploy.yaml](./deploy.yaml)

Sonuç:

- RBAC kaynakları `unchanged` olarak doğrulandı
- `evok8s-scheduler` deployment'ı cluster içinde oluşturuldu

### 3.5. Scheduler pod'unun çalıştığını doğruladık

Deployment sonrası scheduler pod'unun `Running` durumda olduğu görüldü.

Ayrıca loglarda şu başlangıç mesajı doğrulandı:

- `scheduler started name=evok8s-scheduler interval=2s`

Bu, scheduler process'inin cluster içinde başarıyla başladığını gösterdi.

### 3.6. Test workload manifestini oluşturduk

Repo köküne [test-workload.yaml](./test-workload.yaml) dosyası eklendi.

Bu manifest ile:

- `scheduler-smoke` isimli bir `Deployment` oluşturuldu
- `replicas: 3` kullanıldı
- Pod template içinde `schedulerName: evok8s-scheduler` ayarlandı
- Böylece bu pod'ların default scheduler yerine custom scheduler tarafından ele alınması sağlandı

### 3.7. Uçtan uca smoke test yaptık

Test workload cluster'a apply edildi ve ardından şu sonuçlar doğrulandı:

- Test pod'ları `Running` durumuna geçti
- Pod'lar worker node'lara yerleştirildi
- Scheduler loglarında bind mesajları görüldü

Bu nokta çok önemlidir; çünkü ilk kez şu ifade teknik olarak doğrulanmış oldu:

"Kubernetes içinde çalışan custom scheduler'ımız, kendisine yönlendirilen pod'ları gerçek node'lara bind ediyor."

### 3.8. Node filtreleme ekledik ve gerçek cluster üzerinde doğruladık

İlk smoke testten sonra scheduler'ın davranışını Kubernetes scheduling mantığına biraz daha yaklaştırmak için scoring öncesi node filtreleme eklendi.

Kod tarafında şu filtreler eklendi:

- Node `Ready=True` değilse elenir
- Node `spec.unschedulable=true` ise elenir
- Node üzerindeki `NoSchedule` ve `NoExecute` taint'leri pod tarafından tolere edilmiyorsa elenir

Bu değişiklik [scheduler/main.go](./scheduler/main.go) içine eklendi.

Ayrıca davranışı doğrulamak için [scheduler/main_test.go](./scheduler/main_test.go) dosyasında unit testler yazıldı.

Bu testler şu mantıkları doğrular:

- Hazır olmayan node elenir
- Cordoned node elenir
- Toleration olmayan taint'li node elenir
- Uygun toleration varsa node tekrar aday olabilir

`go test ./...` başarılı şekilde geçti.

### 3.9. Güncellenmiş scheduler image'ını yeniden deploy ettik

Node filtreleme eklendikten sonra:

- Scheduler image'ı yeniden build edildi
- Yeni image kind cluster node'larına tekrar yüklendi
- `evok8s-scheduler` deployment'ı rollout restart ile yeniden başlatıldı

Bu sayede cluster içinde çalışan scheduler pod'u yeni filtreleme mantığını kullanmaya başladı.

### 3.10. `cordon` testi yaptık

İlk doğrulama için `evo-sched-worker2` node'u cordon edildi.

Beklenti:

- Scheduler bu node'u artık aday olarak görmemeliydi

Ardından test workload yeniden oluşturuldu ve sonuç şu oldu:

- Tüm yeni pod'lar `evo-sched-worker` üzerine yerleşti
- `evo-sched-worker2` seçilmedi

Bu test, `spec.unschedulable` filtresinin doğru çalıştığını doğruladı.

### 3.11. Taint / toleration testi yaptık

İkinci doğrulama için `evo-sched-worker2` node'una şu taint eklendi:

- `dedicated=test:NoSchedule`

Önce toleration'sız workload yeniden çalıştırıldı.

Sonuç:

- Pod'lar `evo-sched-worker2` yerine yine `evo-sched-worker` üzerine yerleşti

Sonra bu taint'i tolere eden ayrı bir workload tanımlandı:

- [test-workload-toleration.yaml](./test-workload-toleration.yaml)

Bu workload içinde şu toleration vardı:

- `key=dedicated`
- `value=test`
- `effect=NoSchedule`

Sonuç:

- Yeni pod'ların bir kısmı `evo-sched-worker2` üzerine yerleşti
- Scheduler log'larında `worker2` için bind kararları görüldü

Bu test, taint filtresinin ve toleration eşleşmesinin doğru çalıştığını doğruladı.

### 3.12. Test sonrası cluster'ı temizledik

Doğrulama adımlarından sonra test ortamını sade hale getirmek için:

- Test deployment'ları silindi
- `evo-sched-worker2` üzerindeki test taint'i kaldırıldı

Böylece cluster bir sonraki geliştirme adımı için temiz bırakıldı.

### 3.13. Sabit heuristikten parametreli scoring modeline geçtik

Node filtreleme tamamlandıktan sonra scheduler'ın karar verme kısmı sabit `least-pod-count` mantığından çıkarıldı ve ağırlıklı bir scoring modeline taşındı.

Bu değişiklikle birlikte [scheduler/main.go](./scheduler/main.go) içinde şu yapılar eklendi:

- `ResourceRequest`
- `NodeState`
- `ScoreWeights`

Yeni akış şu şekilde çalışır:

1. Tüm node'lar için allocatable CPU/RAM bilgisi çıkarılır
2. O anda node üzerinde bulunan pod'ların toplam requested CPU/RAM değerleri toplanır
3. Her node için `FreeCPU`, `FreeMem` ve `PodCount` türetilir
4. Filter'dan geçen aday node'lar için skor hesaplanır
5. En yüksek skorlu node seçilir

Scoring formülü ilk versiyonda şu bileşenleri kullanır:

- `normFreeCPU`
- `normFreeMem`
- `normPodCount`
- `normPacking`

Skor, env ile verilen ağırlıklarla hesaplanır:

- `WEIGHT_FREE_CPU`
- `WEIGHT_FREE_MEM`
- `WEIGHT_POD_PENALTY`
- `WEIGHT_PACKING`

Bu değişiklik scheduler'ı evolutionary optimization için uygun hale getiren en kritik refactor adımıdır; çünkü artık davranış kod içine gömülü bir kural değil, dışarıdan ayarlanabilen bir policy yüzeyidir.

### 3.14. Resource fit kontrolü ekledik

Scoring refactor'u sırasında sadece puanlama eklenmedi; aynı zamanda pod request'inin node'a sığıp sığmadığı da kontrol edilmeye başlandı.

Bu sayede:

- CPU veya memory açısından yetmeyen node'lar artık seçilmiyor
- `packing` skoru yalnızca gerçekten pod'u alabilecek node'lar arasında değerlendiriliyor

Bu, parametrik scoring'in yanlış problem üzerinde çalışmasını engelleyen önemli bir düzeltmedir.

### 3.15. Scoring davranışı için unit testler ekledik

[scheduler/main_test.go](./scheduler/main_test.go) dosyası genişletildi.

Unit testlerde şu davranışlar doğrulandı:

- Pod container request'leri doğru toplanıyor mu
- Weight'ler env'den doğru okunuyor mu
- `FreeCPU` baskınsa daha boş node seçiliyor mu
- `PodPenalty` baskınsa daha az pod'lu node seçiliyor mu
- `Packing` baskınsa daha dolu ama sığan node seçiliyor mu
- Hiçbir node pod'u sığdıramıyorsa seçim boş dönüyor mu

`go test ./...` başarılı şekilde geçti.

### 3.16. Deployment manifestine scoring weight env'leri ekledik

[deploy.yaml](./deploy.yaml) güncellendi.

Deployment içine varsayılan scoring weight'leri env olarak eklendi:

- `WEIGHT_FREE_CPU=1.0`
- `WEIGHT_FREE_MEM=1.0`
- `WEIGHT_POD_PENALTY=0.5`
- `WEIGHT_PACKING=0.2`

Bu sayede scheduler pod'u cluster içinde çalışırken ağırlıklarını kod değişmeden dışarıdan alabilir hale geldi.

### 3.17. Güncellenmiş scheduler'ı cluster'a deploy edip smoke test yaptık

Refactor sonrası:

- Scheduler image'ı yeniden build edildi
- kind node'larına yüklendi
- Deployment yeniden apply edildi
- Scheduler rollout'u başarıyla tamamlandı

Loglarda scheduler'ın yeni weight'lerle başladığı doğrulandı:

- `weights={FreeCPU:1 FreeMem:1 PodPenalty:0.5 Packing:0.2}`

Ardından smoke workload tekrar çalıştırıldı.

Sonuç:

- Pod'lar başarıyla schedule edildi
- Scheduler log'larında artık bind kararlarıyla birlikte skor da yazıldı

Bu test, parametrik scoring refactor'unun canlı cluster'da regresyon yaratmadan çalıştığını doğruladı.

### 3.18. İlk evaluation harness iskeletini ekledik

Scoring refactor'undan sonra sıradaki ihtiyaç, farklı weight set'lerini aynı workload üzerinde tekrar tekrar koşturup ölçüm alabilecek bir evaluation katmanıydı.

Bu amaçla repo içine şu yapılar eklendi:

- [scheduler/cmd/evaluator/main.go](./scheduler/cmd/evaluator/main.go)
- [scheduler/pkg/evaluator/types.go](./scheduler/pkg/evaluator/types.go)
- [scheduler/pkg/evaluator/workload.go](./scheduler/pkg/evaluator/workload.go)
- [scheduler/pkg/evaluator/weights.go](./scheduler/pkg/evaluator/weights.go)
- [scheduler/pkg/evaluator/metrics.go](./scheduler/pkg/evaluator/metrics.go)
- [workloads/uniform-small.json](./workloads/uniform-small.json)

İlk evaluator sürümü şunları yapar:

1. JSON workload dosyasını okur
2. İstenirse scheduler deployment weight'lerini günceller
3. Workload pod'larını sırayla cluster'a oluşturur
4. Pod scheduling ve completion zamanlarını toplar
5. Özet metrikleri JSON olarak üretir
6. Test pod'larını sonunda temizler

Bu ilk sürümde üretilen temel metrikler:

- `averagePendingSeconds`
- `p95PendingSeconds`
- `unschedulableCount`
- `maxActiveNodes`

### 3.19. Workload formatını sabitledik

İlk harness için workload formatı sade bir JSON liste olarak seçildi.

Her workload item şu alanları içerir:

- `name`
- `arrivalSeconds`
- `cpuMilli`
- `memoryMiB`
- `durationSeconds`

Bu format evaluator tarafından okunur ve aynı senaryo farklı weight set'leriyle tekrar tekrar oynatılabilir.

### 3.20. Evaluator smoke run yaptık

İlk doğrulama için [workloads/uniform-small.json](./workloads/uniform-small.json) kullanıldı.

Evaluator şu komutla çalıştırıldı:

- `go run ./cmd/evaluator --workload ../workloads/uniform-small.json --timeout 90s --output /tmp/evo-sched-eval.json`

Sonuç:

- Workload başarıyla cluster'a basıldı
- Tüm pod'lar schedule edildi
- Tüm pod'lar tamamlandı
- JSON metrik çıktısı üretildi
- Evaluator namespace'i temiz bırakıldı

Örnek özet sonuç:

- `scheduledCount=4`
- `completedCount=4`
- `unschedulableCount=0`
- `averagePendingSeconds=1.5`
- `p95PendingSeconds=2`
- `maxActiveNodes=2`

Bu, scheduler'ın artık yalnızca çalışan bir sistem değil, aynı zamanda ölçülebilen ve karşılaştırılabilen bir deney nesnesine dönüştüğünü gösterir.

### 3.21. Evaluator'ı batch experiment runner'a çevirdik

Tek koşuluk evaluator, birden fazla weight set'i aynı workload üzerinde art arda koşturabilecek şekilde genişletildi.

Bu amaçla şu eklemeler yapıldı:

- `--weights-file` desteği
- tek JSON içinde çoklu run sonucu üretme
- CSV karşılaştırma çıktısı üretme
- her run öncesi evaluator namespace temizleme
- her run öncesi scheduler weight patch + rollout wait
- batch sonunda scheduler'ı başlangıç weight'lerine geri döndürme

Yeni giriş dosyası formatı için [experiments/weights-smoke.json](./experiments/weights-smoke.json) eklendi.

Bu dosya üç baseline policy içerir:

- `baseline-default`
- `spread-heavy`
- `packing-heavy`

### 3.22. Workload set'ini genişlettik

İlk `uniform-small` workload'una ek olarak iki yeni workload daha tanımlandı:

- [workloads/mixed-batchy.json](./workloads/mixed-batchy.json)
- [workloads/bursty.json](./workloads/bursty.json)

Bu sayede evaluator artık sadece tek tip küçük pod'larla değil, farklı boyutlar ve daha ani yük desenleriyle de çalıştırılabilir.

### 3.23. Batch smoke experiment yaptık

İlk batch doğrulama için şu komut kullanıldı:

- `go run ./cmd/evaluator --workload ../workloads/uniform-small.json --weights-file ../experiments/weights-smoke.json --timeout 90s --output /tmp/evo-batch.json`

Bu koşu sonunda:

- JSON sonuç dosyası üretildi
- CSV karşılaştırma dosyası üretildi
- Üç farklı weight set aynı workload üzerinde art arda koşturuldu
- Evaluator namespace temiz bırakıldı
- Scheduler deployment weight'leri tekrar default değerlerine döndürüldü

Örnek özet çıktı:

- `baseline-default`: `avgPending=1.0`, `p95Pending=1.85`, `maxActiveNodes=2`
- `spread-heavy`: `avgPending=1.5`, `p95Pending=2.0`, `maxActiveNodes=2`
- `packing-heavy`: `avgPending=1.5`, `p95Pending=2.0`, `maxActiveNodes=2`

Bu nokta çok önemli; çünkü artık "aynı workload, farklı weight set, sabit ölçüm çıktısı" döngüsü kurulmuş oldu. Yani evaluator artık gerçek bir experiment runner davranışı sergiliyor.

### 3.24. Capacity-relative workload desteği ekledik

İlk 3x3 deney matrisinden sonra önemli bir sınıra ulaştık:

- weight'ler pending davranışını değiştiriyor
- fakat `maxActiveNodes` metriği mevcut cluster ve hafif workload'larda sabit kalıyor

Bu nedenle workload tanımını cluster kapasitesine daha taşınabilir hale getirdik.

[scheduler/pkg/evaluator/types.go](./scheduler/pkg/evaluator/types.go) ve [scheduler/pkg/evaluator/workload.go](./scheduler/pkg/evaluator/workload.go) içinde şu destek eklendi:

- workload item'larında `cpuFraction`
- workload item'larında `memoryFraction`
- evaluator tarafında worker allocatable kapasitesinden `WorkloadBase` çıkarma
- fractional istekleri gerçek `cpuMilli` ve `memoryMiB` değerlerine çözümleme

Bu tasarımın amacı şudur:

- workload dosyaları sabit makine boyutuna bağımlı kalmaz
- aynı senaryo farklı cluster'larda yeniden üretilebilir hale gelir
- `resource-pressure` workload'ları worker kapasitesinin yüzdesiyle ifade edilebilir

Evaluator batch çıktısına artık şu bilgi de eklenir:

- kullanılan `WorkloadBase`
- resolve edilmiş gerçek workload listesi

Bu sayede deney sonucu yalnızca "hangi dosya çalıştı" değil, "gerçekte hangi pod resource request'leri üretildi" bilgisini de taşır.

### 3.25. Resource-pressure workload ailesini ekledik

Policy farkının `active-node` ekseninde de görünmesi için yeni workload setleri eklendi:

- [workloads/cpu-tight.json](./workloads/cpu-tight.json)
- [workloads/mem-tight.json](./workloads/mem-tight.json)
- [workloads/fragmenting-mix.json](./workloads/fragmenting-mix.json)

Bu workload'lar absolute request yerine worker kapasitesinin yüzdesini kullanır.

Amaçları:

- `cpu-tight`: CPU baskısında packing ve spread davranışını ayırmak
- `mem-tight`: memory baskısında aynı ayrımı görmek
- `fragmenting-mix`: CPU-heavy ve memory-heavy pod'lar birlikteyken oluşan fragmentation riskini gözlemek

Bu workload ailesi, mevcut hafif workload set'lerini çöpe atmaz; aksine onları "light-load baseline" olarak bırakır ve üstüne daha baskılı bir deney katmanı ekler.

### 3.26. 4-worker kind cluster config ekledik

Mevcut cluster'ı bozmadan daha geniş deneyler için ayrı bir kind config dosyası eklendi:

- [kind-config-4workers.yaml](./kind-config-4workers.yaml)

Bu dosya:

- 1 control-plane
- 4 worker

içerir.

Bu tercih şu nedenle yapıldı:

- mevcut çalışan cluster'ı korumak
- ama `packing` ile `spread` arasındaki node-count farkını daha görünür hale getirecek daha büyük bir cluster seçeneğini repo içinde hazır tutmak

İstenirse yeni cluster şu mantıkla oluşturulabilir:

- `kind create cluster --name evo-sched-4w --config kind-config-4workers.yaml`

### 3.27. İlk fractional workload smoke run'ını yaptık

Yeni capacity-relative workload formatını doğrulamak için [workloads/cpu-tight.json](./workloads/cpu-tight.json) ile evaluator çalıştırıldı.

Kullanılan komut:

- `go run ./cmd/evaluator --workload ../workloads/cpu-tight.json --weights-file ../experiments/weights-smoke.json --timeout 150s --output /tmp/evo-cpu-tight.json`

Bu koşuda evaluator mevcut 2-worker cluster'dan şu `WorkloadBase` değerlerini çıkardı:

- `workerCount=2`
- `workerCPUMilli=16000`
- `workerMemoryMiB=31714`

Buna göre `cpu-tight` pod'ları şu gerçek request'lere çözüldü:

- `cpuMilli=7200`
- `memoryMiB=2538`

Sonuç:

- tüm pod'lar schedule edildi
- tüm pod'lar tamamlandı
- evaluator namespace temiz bırakıldı
- scheduler weight'leri tekrar default değerlere döndü

Bu smoke run'ın amacı metric farkı üretmek değil, yeni workload formatının cluster kapasitesine göre doğru çözümlendiğini doğrulamaktı.

### 3.28. 4-worker cluster üzerinde ilk resource-pressure matrix'i koşturduk

Bir sonraki kritik deney için cluster topolojisi 4 worker olacak şekilde yeniden kuruldu.

Uygulanan akış:

- mevcut `evo-sched` kind cluster silindi
- [kind-config-4workers.yaml](./kind-config-4workers.yaml) ile aynı isimde yeni cluster oluşturuldu
- scheduler image'ı yeniden build edildi
- image yeni cluster node'larına yüklendi
- [rbac.yaml](./rbac.yaml) ve [deploy.yaml](./deploy.yaml) yeniden apply edildi
- scheduler pod'unun rollout'u beklendi
- [test-workload.yaml](./test-workload.yaml) ile kısa smoke validation yapıldı

Smoke validation sonucu:

- scheduler pod'u `Running`
- smoke pod'ları başarıyla bind edildi
- bind kararları scheduler log'larında görüldü

Ardından ilk gerçek matrix şu kombinasyonla çalıştırıldı:

- workload'lar: `cpu-tight`, `mem-tight`, `fragmenting-mix`
- weight set'ler: `baseline-default`, `spread-heavy`, `packing-heavy`
- tekrar sayısı: her kombinasyon için `3`

Kontrol disiplini için her batch öncesi ve sonrası şu bilgiler loglandı:

- ready worker sayısı
- scheduler deployment env weight'leri
- evaluator namespace pod sayısı

Bu kontrol log'u şurada saklanır:

- [results/2026-03-15-4worker-resource-pressure/controls.log](./results/2026-03-15-4worker-resource-pressure/controls.log)

Özet sonuçlar şu dosyalara kaydedildi:

- [results/2026-03-15-4worker-resource-pressure/summary.csv](./results/2026-03-15-4worker-resource-pressure/summary.csv)
- [results/2026-03-15-4worker-resource-pressure/summary.json](./results/2026-03-15-4worker-resource-pressure/summary.json)
- [results/2026-03-15-4worker-resource-pressure/detail.csv](./results/2026-03-15-4worker-resource-pressure/detail.csv)

Bu deneyden çıkan ilk kritik bulgular:

- `active-node` tradeoff nihayet açıldı
- `packing-heavy`, `cpu-tight` ve `mem-tight` altında `maxActiveNodes=2` seviyesine inerken `spread-heavy` ve çoğu durumda `baseline-default` `maxActiveNodes=4` kullandı
- `fragmenting-mix` altında `packing-heavy` orta bir davranış göstererek `maxActiveNodes=3` kullandı

Pending tarafındaki okuma:

- `cpu-tight` altında `packing-heavy` ve `spread-heavy` baseline'dan daha düşük pending üretti
- `mem-tight` altında pending metrikleri neredeyse aynı kaldı; fakat `packing-heavy` daha az node ile aynı pending'i verdi
- `fragmenting-mix` altında en düşük pending `spread-heavy`, ikinci `packing-heavy`, en zayıf ise `baseline-default` oldu

Bu sonuç çok önemlidir; çünkü artık scheduler policy weight'leri hem pending hem de node kullanımında görünür tradeoff üretmektedir. Yani proje gerçekten çok hedefli scheduler optimization karakterine ulaşmıştır.

### 3.29. Optimizer iskeletini ekledik

Tradeoff yüzeyi deneysel olarak doğrulandıktan sonra bir sonraki doğal adım evolutionary search katmanını eklemekti.

Bu amaçla:

- evaluator runtime'ı [scheduler/pkg/evaluator/runner.go](./scheduler/pkg/evaluator/runner.go) içine çıkarıldı
- [scheduler/cmd/evaluator/main.go](./scheduler/cmd/evaluator/main.go) ince bir CLI wrapper haline getirildi
- yeni [scheduler/pkg/optimizer](./scheduler/pkg/optimizer) paketi eklendi
- yeni [scheduler/cmd/optimizer/main.go](./scheduler/cmd/optimizer/main.go) komutu eklendi

Optimizer tarafında ilk sürüm şu yapıyı kullanır:

- birey: `FreeCPU`, `FreeMem`, `PodPenalty`, `Packing`
- objective 1: workload'lar üzerindeki ortalama `p95PendingSeconds`
- objective 2: workload'lar üzerindeki ortalama `maxActiveNodes`
- selection: nondominated sort + crowding distance
- variation: uniform crossover + gaussian mutation
- elitism: NSGA-II benzeri population update

Bu tasarım sayesinde optimizer artık gerçek cluster evaluator'ını doğrudan çağırabilir.

Yani kavramsal olarak:

- `evaluate(weights) -> {avgP95Pending, avgMaxActiveNodes}`

akışı kod içinde kurulmuş oldu.

Optimizer komutu baseline policy'leri de aynı çıktı içine dahil eder:

- `baseline-default`
- `spread-heavy`
- `packing-heavy`

ve bunları optimizer adaylarıyla aynı Pareto analizine koyar.

### 3.30. İlk optimizer smoke run'ını yaptık

Kod entegrasyonunu doğrulamak için küçük bir smoke run çalıştırıldı.

Kullanılan komut:

- `go run ./cmd/optimizer --workloads ../workloads/cpu-tight.json --baseline-file ../experiments/weights-smoke.json --population 2 --generations 1 --seed 42 --timeout 120s --output /tmp/evo-optimizer-smoke.json`

Bu smoke run sonunda:

- JSON sonuç dosyası üretildi
- combined Pareto CSV üretildi
- all-candidates CSV üretildi
- evaluator namespace temiz kaldı
- scheduler weight'leri tekrar default değerlere döndü

Bu aşamanın amacı tam ölçekli optimization sonucu almak değil, evolutionary loop'un evaluator entegrasyonu ile cluster üstünde gerçekten çalıştığını doğrulamaktı.

### 3.31. Scheduler deployment weight update akışını conflict retry ile sağlamlaştırdık

İlk uzun optimizer koşusunda scheduler deployment env'leri güncellenirken ara sıra Kubernetes `resourceVersion` conflict hatası görüldü.

Hata biçimi kabaca şuydu:

- `Operation cannot be fulfilled on deployments.apps "evok8s-scheduler": the object has been modified`

Bu durum, evaluator'ın scheduler deployment'ını `Get` edip `Update` etmesi ile deployment controller'ın aynı nesneyi eşzamanlı güncellemesi arasında yarış oluşabildiğini gösterdi.

Bunu düzeltmek için [scheduler/pkg/evaluator/runner.go](./scheduler/pkg/evaluator/runner.go) içindeki `applyWeights(...)` akışı güçlendirildi:

- deployment her denemede yeniden `Get` edilir
- `Update` sırasında conflict alınırsa kısa backoff ile tekrar denenir
- en fazla 5 denemeden sonra hata döndürülür

Ardından optimizer smoke run'ı tekrar çalıştırıldı:

- `go run ./cmd/optimizer --workloads ../workloads/cpu-tight.json --baseline-file ../experiments/weights-smoke.json --population 2 --generations 1 --seed 42 --timeout 120s --output /tmp/evo-optimizer-smoke-retry.json`

Sonuç:

- smoke run başarıyla tamamlandı
- JSON ve CSV çıktıları üretildi
- evaluator namespace temiz kaldı
- scheduler weight'leri tekrar default değerlere restore edildi

Bu düzeltme, uzun optimizer deneylerinin deployment update çakışmaları yüzünden yarıda kalma riskini belirgin biçimde azalttı.

### 3.32. Danışman geri bildirimi ile araştırma yönünü netleştirdik

Bu aşamada danışmandan proje kapsamı, ana objective'ler ve dönem sonu için yeterli başarı ölçütü hakkında net geri bildirim alındı.

Sorulan ana sorular ve alınan cevaplar özetle şöyledir:

1. Ana araştırma çıktısı ne olmalı?

- Ana çıktı `Pareto front` olmalı.
- Tek bir birleşik skor ana çıktı olarak kullanılmamalı.
- İstenirse raporda yardımcı analiz olarak tek bir "uzlaşma skoru" verilebilir.

2. Hangi metrikler ana objective olarak tutulmalı?

- Ana objective'ler:
  - `p95 pending time`
  - `max active nodes`
- Yardımcı metrikler:
  - `unschedulable count/rate`
  - `load variance`

3. Baseline set'i yeterli mi?

- Mevcut üç baseline bu dönem için yeterli kabul edildi:
  - `baseline-default`
  - `spread-heavy`
  - `packing-heavy`
- `default kube-scheduler` ile karşılaştırma değerli bir güçlendirme olur, ama bu dönem için zorunlu değil.

4. Sentetik workload yeterli mi?

- Evet, bu dönem için yeterli.
- Mevcut sentetik workload'lar tekrar üretilebilir ve tradeoff'u görünür kılıyor.
- Gerçek trace tabanlı workload, gelecekteki genişletme olarak düşünülebilir.

5. Kaç tekrar yeterli?

- Matrix deneyleri için `3` tekrar yeterli kabul edildi.
- Optimizer için en az `3` seed önerildi:
  - `42`
  - `123`
  - `999`
- Zaman kalırsa `5` seed daha güçlü raporlama sağlar.

6. Optimizer tarafında hedef tek çözüm mü, çözüm kümesi mi?

- Hedef tek en iyi çözüm değil, `Pareto kümesi` olmalı.
- Rapor içinde bu kümeden üç temsilci çözüm seçilmeli:
  - pending-odaklı en iyi çözüm
  - node-odaklı en iyi çözüm
  - dengeli uzlaşma çözümü
- Dengeli uzlaşma çözümü için ana seçim yöntemi `knee point` olacak.
- Gerekirse normalize edilmiş birleşik skor yalnızca ikincil doğrulama için kullanılacak.

7. Bu dönem için makul bitiş noktası nedir?

- Çalışan gerçek custom scheduler
- Tradeoff'un deneysel olarak gösterilmesi
- Evolutionary search ile baseline'ların ötesinde yeni Pareto noktaları bulunması

8. kind üzerinde deney yapmak metodolojik olarak yeterli mi?

- Evet, kontrollü ve tekrar üretilebilir Kubernetes testbed olarak yeterli kabul edildi.
- Raporda açıkça şu sınır yazılmalı:
  - sonuçlar metodolojik karşılaştırma içindir
  - üretim ortamına doğrudan genelleme iddia edilmemektedir

9. Raporlama ağırlığı sistem mi, optimizasyon mu olmalı?

- Yaklaşık denge:
  - `%35` sistem altyapısı
  - `%65` optimizasyon ve deney metodolojisi

10. Projedeki ana akademik değer nedir?

- Ana değer: workload'a göre optimize edilen scheduling policy
- Çarpan etkisi: bu policy'nin gerçek Kubernetes scheduler olarak çalışıyor olması

Bu görüşmeden sonra netleşen kararlar şunlardır:

- Ana analiz ekseni `Pareto front` olacak.
- Ana objective set'i sabitlendi:
  - `p95 pending time`
  - `max active nodes`
- Yardımcı metrikler raporda tutulacak:
  - `unschedulable count/rate`
  - `load variance`
- Bu dönem için sentetik workload yeterli kabul edildi.
- Bu dönem için mevcut üç baseline yeterli kabul edildi.
- `default kube-scheduler` karşılaştırması opsiyonel güçlendirme olarak bırakıldı.
- Bu dönem için hedef, evolutionary optimizer'ın baseline'ların ötesinde yeni Pareto noktaları bulduğunu göstermek.
- Pareto içinden seçilecek dengeli temsilci çözüm `knee point` yaklaşımı ile belirlenecek.
- Tam optimizer değerlendirmesi en az üç seed ile koşturulacak:
  - `42`
  - `123`
  - `999`

### 3.33. İlk tam optimizer koşusunu (`seed=42`) tamamlayıp özetledik

Danışman geri bildirimi sonrasında ilk tam optimizer koşusu şu ayarlarla tamamlandı:

- population size: `12`
- generations: `5`
- workloads:
  - `cpu-tight`
  - `mem-tight`
  - `fragmenting-mix`
- seed: `42`

Bu koşunun çıktıları şu klasöre kaydedildi:

- [results/2026-03-15-optimizer-seed42/result.json](./results/2026-03-15-optimizer-seed42/result.json)
- [results/2026-03-15-optimizer-seed42/result-pareto.csv](./results/2026-03-15-optimizer-seed42/result-pareto.csv)
- [results/2026-03-15-optimizer-seed42/result-all.csv](./results/2026-03-15-optimizer-seed42/result-all.csv)

Toplam değerlendirme sayıları:

- baseline sayısı: `3`
- optimizer tarafından değerlendirilen aday sayısı: `72`
- baseline dahil toplam çözüm sayısı: `75`
- final population boyutu: `12`
- optimizer-only nondominated çözüm sayısı: `3`
- baseline dahil birleşik nondominated çözüm sayısı: `4`

Birleşik Pareto front'ta kalan çözümler:

- `gen-03-008`
- `gen-03-009`
- `gen-05-012`
- `packing-heavy`

Ancak raporlama açısından daha önemli olan şey, birey sayısı değil benzersiz objective noktalarıdır.

`seed=42` için birleşik Pareto front iki benzersiz tradeoff noktasına çöktü:

1. `avgP95Pending = 0.6667`, `avgMaxActiveNodes = 4`
2. `avgP95Pending = 1.0000`, `avgMaxActiveNodes = 2.3333`

Bu sonuç, Pareto yüzeyinin bu seed için iki baskın rejime ayrıldığını gösterdi:

- düşük pending / yüksek node kullanımı
- daha düşük node kullanımı / biraz daha yüksek pending

Baseline durumu:

- Pareto'da kalan baseline:
  - `packing-heavy`
- Pareto'dan düşen baseline'lar:
  - `baseline-default`
  - `spread-heavy`

Özellikle `spread-heavy` objective noktasının optimizer tarafından domine edildiği görüldü.

Yeni optimizer bulgusu:

- Optimizer, `packing-heavy` ile aynı düşük-node rejimini yeniden keşfetti:
  - `gen-05-012`
- Ayrıca baseline set'te olmayan yeni bir düşük-pending rejimi buldu:
  - `gen-03-008`
  - `gen-03-009`

Bu yeni rejim şu objective noktasını verdi:

- `avgP95Pending = 0.6667`
- `avgMaxActiveNodes = 4`

Temsilci çözümler:

- pending-odaklı en iyi çözüm:
  - `gen-03-008` veya `gen-03-009`
- node-odaklı en iyi çözüm:
  - `packing-heavy` veya aynı objective noktayı bulan `gen-05-012`

`seed=42` için strict anlamda tekil bir `knee point` çıkmadı.
Sebep, birleşik Pareto front'un yalnızca iki benzersiz objective noktasından oluşmasıdır.

Bu nedenle bu seed için çıkarılan ana araştırma cümlesi şudur:

- optimizer, baseline set'in ötesinde yeni bir baskın düşük-pending rejimi buldu
- düşük-node rejiminde ise `packing-heavy` davranışını yeniden keşfetti
- Pareto yapısı bu seed altında iki baskın tradeoff rejimine ayrıldı
  - düşük pending / yüksek node kullanımı
  - daha düşük node kullanımı / biraz daha yüksek pending

## 4. Şu Anki Durum

Şu anda proje durumu şöyledir:

- 1. katmanın çekirdek akışı çalışıyor
- Custom scheduler cluster içinde deploy edilmiş durumda
- `schedulerName: evok8s-scheduler` kullanan pod'lar custom scheduler tarafından schedule edilebiliyor
- Scheduler artık uygun olmayan node'ları filtreliyor
- Scheduler artık parametreli bir skor fonksiyonu ile node seçiyor
- Skor ağırlıkları env üzerinden değiştirilebiliyor
- Resource fit kontrolü scoring akışına dahil edilmiş durumda
- Birden fazla weight set'i art arda koşturabilen batch evaluator mevcut
- JSON ve CSV sonuç üretilebiliyor
- Çoklu workload set'i tanımlanmış durumda
- Workload'lar artık worker kapasitesine göre yüzdesel olarak tanımlanabiliyor
- `resource-pressure` workload ailesi repo'ya eklenmiş durumda
- Daha büyük deneyler için 4-worker cluster config hazır durumda
- 4-worker cluster üzerinde ilk resource-pressure matrix'i başarıyla koşturulmuş durumda
- Sonuçlar repo altında saklanmış durumda
- Optimizer iskeleti eklenmiş durumda
- Optimizer, gerçek evaluator'ı kullanarak cluster üstünde aday değerlendirebiliyor
- Danışman geri bildirimi ile ana objective ve raporlama yönü netleşmiş durumda
- Yardımcı metrik olarak `load variance` seçilmiş durumda
- Pareto içinden dengeli temsilci çözüm için `knee point` yaklaşımı seçilmiş durumda
- Tam ölçekli ilk optimizer koşusu (`seed=42`) tamamlanmış ve özetlenmiş durumda
- `seed=42` altında iki baskın Pareto rejimi gözlenmiş durumda

Başka bir ifadeyle:

- Adım 1 tamam
- Adım 2 tamam
- Adım 3 tamam
- Adım 4 ilk başarılı smoke test seviyesiyle tamam
- Adım 4 için temel feasibility filtreleri de eklenmiş durumda
- Adım 5 için gereken parametrik policy zemini kurulmuş durumda
- Adım 6 ve 7 için gerekli evaluation altyapısının ilk kullanışlı sürümü kurulmuş durumda
- Adım 6 için hafif workload'lara ek olarak baskılı workload ailesi de hazırlanmış durumda
- Adım 6 ve 7 için node-count tradeoff'u görünür hale gelmiş durumda
- Adım 8 için gerekli evolutionary loop iskeleti kurulmuş durumda
- Adım 8 için ilk tam seed sonucu alınmış ve Pareto özeti çıkarılmış durumda

## 5. Sıradaki Muhtemel Adımlar

Buradan sonra mantıklı ilerleme sırası şudur:

1. Tam optimizer koşularını tamamlamak:
   - `seed=123`
   - `seed=999`
2. `seed=123` ve `seed=999` altında da benzersiz Pareto noktalarını çıkarmak
3. Her seed için Pareto front'u baseline policy'lerle doğrudan karşılaştırmak
4. Pareto kümesinden üç temsilci çözüm seçmek:
   - pending-odaklı en iyi
   - node-odaklı en iyi
   - `knee point` ile seçilen dengeli uzlaşma çözümü
5. Yardımcı metrikleri sonuç tablolarına eklemek:
   - `unschedulable`
   - `load variance`
6. Sonuçları tablo/grafik üretimine bağlamak ve rapor anlatısını kurmak

İlk teknik iyileştirme adayları:

- Scheduler cadence etkisini ayırmak için tiny-control workload eklemek
- Yardımcı metrik olarak `load variance` hesaplamasını evaluator'a eklemek
- Farklı bireylerin aynı objective noktasına çökmesini dikkate alarak raporlamayı benzersiz Pareto noktaları üzerinden yapmak

## 6. Bu Dosya Nasıl Kullanılmalı

Her anlamlı session sonunda bu dosya güncellenmelidir.

Önerilen güncelleme düzeni:

- Önce amaç ve büyük resim sabit tutulur
- Sonra "bu session öncesi durum" kısa şekilde revize edilir
- Ardından yeni session'da yapılan somut işler eklenir
- En altta güncel durum ve sıradaki adım net biçimde yazılır

Bu sayede dosya hem ilerleme günlüğü hem de projeye geri döndüğümüzde hızlı onboarding rehberi olarak kullanılabilir.
